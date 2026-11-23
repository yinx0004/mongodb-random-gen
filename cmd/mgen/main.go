// Command mgen generates realistic random data for MongoDB from a simple YAML file.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/yinx0004/mongodb-random-gen/internal/config"
	"github.com/yinx0004/mongodb-random-gen/internal/gen"
	"github.com/yinx0004/mongodb-random-gen/internal/i18n"
	"github.com/yinx0004/mongodb-random-gen/internal/runner"
	"github.com/yinx0004/mongodb-random-gen/internal/sink"
)

var version = "0.1.0"

//go:embed templates/*.yaml
var templates embed.FS

const (
	defaultURI     = "mongodb://localhost:27017"
	defaultWorkers = 8
)

// errInterrupted is returned after Ctrl-C; the message has already been printed.
var errInterrupted = errors.New("interrupted")

// stderrMu serializes the progress bar and retry messages written by workers.
var stderrMu sync.Mutex

// retries counts batch retries of the collection being written.
var retries atomic.Int64

func main() {
	root := newRoot()
	// `mgen users.yaml` is short for `mgen run users.yaml`.
	if len(os.Args) > 1 {
		first := os.Args[1]
		known := first == "help" || first == "completion" || strings.HasPrefix(first, "-")
		for _, c := range root.Commands() {
			if c.Name() == first {
				known = true
			}
		}
		if !known {
			os.Args = append([]string{os.Args[0], "run"}, os.Args[1:]...)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // first Ctrl-C finishes in-flight batches; a second one exits immediately
	}()
	if err := root.ExecuteContext(ctx); err != nil {
		if errors.Is(err, errInterrupted) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, i18n.T("错误：", "error: ")+err.Error())
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:     "mgen",
		Short:   i18n.T("简单易用的 MongoDB 随机数据生成器", "Easy random data generator for MongoDB"),
		Version: version,
		Long: i18n.T(`简单易用的 MongoDB 随机数据生成器

  mgen init                 生成一个带注释的示例配置 mgen.yaml
  mgen preview mgen.yaml    预览几条数据（不连数据库）
  mgen mgen.yaml            写入 MongoDB（run 可省略）
  mgen types                查看全部类型和示例`, `Easy random data generator for MongoDB

  mgen init                 create a commented example config, mgen.yaml
  mgen preview mgen.yaml    print a few documents (no database needed)
  mgen mgen.yaml            write to MongoDB (same as mgen run mgen.yaml)
  mgen types                list all types with examples`),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newRun(), newPreview(), newTypes(), newInit())
	return root
}

// ---------------------------------------------------------------------------
// shared
// ---------------------------------------------------------------------------

type compiled struct {
	coll *config.Collection
	doc  *gen.Doc
}

func load(path, locale string) (*config.Config, []compiled, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	if locale != "" {
		m, ok := gen.ParseLocaleMode(locale)
		if !ok {
			return nil, nil, i18n.Errorf("--locale 只能是 zh、en 或 mixed", "--locale must be zh, en or mixed")
		}
		for _, c := range cfg.Collections {
			c.Locale = m
		}
	}
	var out []compiled
	for _, c := range cfg.Collections {
		d, err := gen.CompileDoc(c.Fields, c.Name, c.Locale)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, compiled{c, d})
	}
	return cfg, out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fmtInt(n int) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// ---------------------------------------------------------------------------
// run
// ---------------------------------------------------------------------------

func newRun() *cobra.Command {
	var count, batch, workers, maxRetries int
	var uri, db, out, locale, wcFlag string
	var drop bool
	cmd := &cobra.Command{
		Use:   "run <config.yaml>",
		Short: i18n.T("按配置生成数据并写入 MongoDB（run 可省略）", "generate data and write it to MongoDB (`run` is optional)"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, colls, err := load(args[0], locale)
			if err != nil {
				return err
			}
			dbName := firstNonEmpty(db, cfg.DB, "test")
			if cmd.Flags().Changed("count") {
				for _, c := range colls {
					c.coll.Count = count
				}
			}
			if batch < 1 {
				batch = 1000
			}
			if workers < 1 {
				return i18n.Errorf("--workers 至少为 1", "--workers must be at least 1")
			}
			if maxRetries < 0 {
				return i18n.Errorf("--retries 不能为负数", "--retries must not be negative")
			}

			var s sink.Sink
			var dir *sink.JSONLDir
			var hintURI string // set when writing to MongoDB, for timeout hints
			if out != "" {
				fi, statErr := os.Stat(out)
				isDir := strings.HasSuffix(out, "/") || (statErr == nil && fi.IsDir()) || filepath.Ext(out) == ""
				switch {
				case out == "-":
					s = sink.NewJSONL(os.Stdout, nil)
				case isDir:
					dir = &sink.JSONLDir{Dir: out}
					s = dir
				case len(colls) > 1:
					return i18n.Errorf("配置里有 %d 个集合，--out 请给一个目录（如 --out ./data），每个集合会写成单独的 <集合名>.jsonl",
						"the config has %d collections; give --out a directory (e.g. --out ./data) to get one <collection>.jsonl per collection", len(colls))
				default:
					f, err := os.Create(out)
					if err != nil {
						return err
					}
					s = sink.NewJSONL(f, f)
				}
			} else {
				u := firstNonEmpty(uri, cfg.URI, os.Getenv("MONGO_URI"), defaultURI)
				wc, err := writeConcern(wcFlag, cmd.Flags().Changed("write-concern"), u)
				if err != nil {
					return err
				}
				m, err := sink.ConnectMongo(u, dbName, sink.MongoOptions{
					WriteConcern: wc, Workers: workers, Retries: maxRetries, OnRetry: printRetry,
				})
				if err != nil {
					return sink.ErrConnect(u, err)
				}
				s, hintURI = m, u
			}

			start := time.Now()
			for _, c := range colls {
				opt := runner.Options{Workers: workers, Batch: batch, Seed: rand.Uint64()}
				if err := writeCollection(cmd.Context(), s, dbName, c, drop || c.coll.Drop, hintURI, opt); err != nil {
					s.Close()
					return err
				}
			}
			if err := s.Close(); err != nil {
				return err
			}
			if out != "" && out != "-" {
				fmt.Fprintln(os.Stderr, i18n.T("已写入文件，可以这样导入：", "files written; import with:"))
				if dir != nil {
					for i, p := range dir.Files {
						fmt.Fprintf(os.Stderr, "  mongoimport --db %s --collection %s --file %s\n", dbName, colls[i].coll.Name, p)
					}
				} else {
					fmt.Fprintf(os.Stderr, "  mongoimport --db %s --collection %s --file %s\n", dbName, colls[0].coll.Name, out)
				}
			}
			if len(colls) > 1 {
				fmt.Fprintln(os.Stderr, i18n.Tf("全部完成，用时 %.1fs", "all done in %.1fs", time.Since(start).Seconds()))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVarP(&count, "count", "n", config.DefaultCount, i18n.T("每个集合生成多少条（覆盖配置里的 count）", "documents per collection (overrides `count` in the config)"))
	f.StringVar(&uri, "uri", "", i18n.T("MongoDB 连接串，默认读配置、$MONGO_URI，再默认 "+defaultURI, "MongoDB URI; defaults to the config, then $MONGO_URI, then "+defaultURI))
	f.StringVar(&db, "db", "", i18n.T("数据库名（覆盖配置），默认 test", "database name (overrides the config), default test"))
	f.BoolVar(&drop, "drop", false, i18n.T("写入前先清空集合", "drop each collection before writing"))
	f.StringVarP(&out, "out", "o", "", i18n.T("不写库，输出 JSON Lines：给目录则每个集合一个文件，给 .jsonl 文件名则写单个集合，- 表示屏幕",
		"write JSON Lines instead of MongoDB: a directory gets one file per collection, a .jsonl path takes a single collection, - is stdout"))
	f.StringVar(&locale, "locale", "", i18n.T("覆盖配置里的 locale：zh | en | mixed", "override the config locale: zh | en | mixed"))
	f.IntVar(&batch, "batch", 1000, i18n.T("每批写入条数", "documents per insert batch"))
	f.IntVarP(&workers, "workers", "w", defaultWorkers, i18n.T("并发写入的 worker 数", "number of concurrent writers"))
	f.IntVar(&maxRetries, "retries", 5, i18n.T("一批遇到网络错误或超时时最多重试几次（间隔约 1s、2s、4s、8s、15s，共约 30s，足够等副本集选主）",
		"retries per batch on network errors or timeouts (about 1s, 2s, 4s, 8s, 15s: ~30s in total, enough for a replica set election)"))
	f.StringVar(&wcFlag, "write-concern", "1", i18n.T(
		"写入确认级别：1（默认，主节点确认即返回）| majority（等多数节点）| 0（不等确认，失败不会报告）。连接串里写了 w= 时以连接串为准，除非显式指定本参数",
		"write concern: 1 (default, primary only) | majority | 0 (fire and forget, failures are not reported). A w= in the URI wins unless this flag is given"))
	return cmd
}

// writeConcern decides the write concern: an explicit flag wins, then a w= in
// the URI (nil keeps it), then w:1. MongoDB 5.0+ replica sets default to
// majority, which is much slower for bulk loading test data.
func writeConcern(flag string, explicit bool, uri string) (*writeconcern.WriteConcern, error) {
	if !explicit {
		if u, err := url.Parse(uri); err == nil {
			for k := range u.Query() {
				if strings.EqualFold(k, "w") {
					return nil, nil
				}
			}
		}
		return writeconcern.W1(), nil
	}
	if strings.EqualFold(flag, "majority") {
		return writeconcern.Majority(), nil
	}
	n, err := strconv.Atoi(flag)
	if err != nil || n < 0 {
		return nil, i18n.Errorf("--write-concern 只能是 majority 或非负整数，收到 %q", "--write-concern must be majority or a non-negative integer, got %q", flag)
	}
	return &writeconcern.WriteConcern{W: n}, nil
}

// printRetry reports a batch retry above the progress bar.
func printRetry(coll string, attempt int, wait time.Duration, err error) {
	retries.Add(1)
	stderrMu.Lock()
	defer stderrMu.Unlock()
	fmt.Fprintf(os.Stderr, "\r\033[K%s\n", i18n.Tf("⚠ 写入 %s 出错，%.1fs 后第 %d 次重试：%s",
		"⚠ writing %s failed, retrying in %.1fs (attempt %d): %s", coll, wait.Seconds(), attempt, clip(err.Error(), 160)))
}

// writeCollection writes one collection. uri is non-empty when writing to
// MongoDB and is only used to explain timeouts.
func writeCollection(ctx context.Context, s sink.Sink, dbName string, c compiled, drop bool, uri string, opt runner.Options) error {
	toDB := uri != ""
	retries.Store(0)
	name, total := c.coll.Name, c.coll.Count
	full := dbName + "." + name
	if err := s.Begin(name, drop && toDB); err != nil {
		return err
	}
	if drop && toDB {
		fmt.Fprintln(os.Stderr, i18n.Tf("已清空 %s", "dropped %s", full))
	}
	start := time.Now()
	showProgress := isTerminal(os.Stderr) && total > 0
	if showProgress {
		opt.Progress = func(done int64) {
			stderrMu.Lock()
			defer stderrMu.Unlock()
			pct := float64(done) * 100 / float64(total)
			rate := float64(done) / max(time.Since(start).Seconds(), 1e-9)
			bar := strings.Repeat("█", int(pct/4)) + strings.Repeat("░", 25-int(pct/4))
			fmt.Fprintf(os.Stderr, "\r\033[K%s %s %5.1f%%  %s/%s  %s%s",
				full, bar, pct, fmtInt(int(done)), fmtInt(total), fmtInt(int(rate)), i18n.T(" 条/秒", " docs/s"))
		}
	}
	res, err := runner.Run(ctx, s, c.doc, total, opt)
	if showProgress {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			written := fmtInt(int(res.Done - res.Failed))
			fmt.Fprintln(os.Stderr, i18n.T(fmt.Sprintf("已中断：%s 已写入 %s 条", full, written),
				fmt.Sprintf("interrupted: %s docs written to %s", written, full)))
			return errInterrupted
		}
		msg := i18n.Tf("写入 %s 失败（已写入 %s 条）：%v", "writing %s failed (%s docs written): %v", full, fmtInt(int(res.Done-res.Failed)), err)
		if re := (*sink.RetryError)(nil); errors.As(err, &re) {
			msg += i18n.Tf("\n（这一批已重试 %d 次仍失败）", "\n(this batch still failed after %d retries)", re.Retries)
		}
		if h := sink.Hint(uri, err); toDB && h != "" {
			msg += "\n" + h
		}
		return errors.New(msg)
	}
	el := time.Since(start)
	rate := float64(total) / max(el.Seconds(), 1e-9)
	msg := i18n.Tf("✓ %s  %s 条  %.1fs  (%s 条/秒)", "✓ %s  %s docs  %.1fs  (%s docs/s)", full, fmtInt(total), el.Seconds(), fmtInt(int(rate)))
	if res.Failed > 0 {
		msg += i18n.Tf("，其中 %s 条写入失败（通常是唯一索引冲突）", ", %s failed to insert (usually unique index conflicts)", fmtInt(int(res.Failed)))
	}
	if n := retries.Load(); n > 0 {
		msg += i18n.Tf("，期间重试 %d 次", ", %d batch retries", n)
	}
	fmt.Fprintln(os.Stderr, msg)
	return nil
}

// ---------------------------------------------------------------------------
// preview
// ---------------------------------------------------------------------------

func newPreview() *cobra.Command {
	var n int
	var locale, db string
	cmd := &cobra.Command{
		Use:   "preview <config.yaml>",
		Short: i18n.T("打印几条样例数据，不连数据库", "print a few sample documents without touching the database"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, colls, err := load(args[0], locale)
			if err != nil {
				return err
			}
			dbName := firstNonEmpty(db, cfg.DB, "test")
			for i, c := range colls {
				if i > 0 {
					fmt.Println()
				}
				k := min(n, c.coll.Count)
				stream := c.doc.Stream(rand.Uint64(), 0)
				fmt.Println(i18n.Tf("// %s.%s：预览 %d 条（实际会生成 %s 条）", "// %s.%s: %d sample(s), %s will be generated",
					dbName, c.coll.Name, k, fmtInt(c.coll.Count)))
				for j := 0; j < k; j++ {
					b, err := bson.MarshalExtJSONIndent(stream.Next(), false, false, "", "  ")
					if err != nil {
						return err
					}
					fmt.Println(string(b))
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "n", "n", 3, i18n.T("每个集合打印几条", "documents to print per collection"))
	cmd.Flags().StringVar(&locale, "locale", "", i18n.T("覆盖配置里的 locale：zh | en | mixed", "override the config locale: zh | en | mixed"))
	cmd.Flags().StringVar(&db, "db", "", i18n.T("数据库名（只影响显示）", "database name (display only)"))
	return cmd
}

// ---------------------------------------------------------------------------
// types
// ---------------------------------------------------------------------------

func newTypes() *cobra.Command {
	return &cobra.Command{
		Use:   "types",
		Short: i18n.T("列出全部类型和示例值", "list all types with example values"),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			r := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 1))
			group := gen.Group(-1)
			for _, t := range gen.Types {
				if t.Group != group {
					group = t.Group
					fmt.Printf("\n== %s ==\n", group.Title())
				}
				head := t.Name
				if t.Usage != "" {
					head = t.Usage
				}
				if len(t.Aliases) > 0 {
					head += "  (" + strings.Join(t.Aliases, ", ") + ")"
				}
				fmt.Printf("  %s\n      %s\n", head, i18n.T(t.DescZh, t.DescEn))
				if t.Localized {
					fmt.Printf("      zh: %s\n      en: %s\n", clip(gen.Example(t, gen.Zh, r), 70), clip(gen.Example(t, gen.En, r), 70))
				} else {
					fmt.Printf("      %s %s\n", i18n.T("例:", "e.g."), clip(gen.Example(t, gen.Zh, r), 70))
				}
			}
			fmt.Print(i18n.T(`
== 写法速查 ==
  age: 18..60                     整数范围
  price: 0..99.90                 小数范围，小数位数照抄
  createdAt: 2023-01-01..now      时间范围；也支持 today、-30d、-6mo、-1y、+7d
  level: [normal 80%, vip 20%]    从列表里选，可写权重
  source: [app]                   固定字符串
  version: 2                      数字 / true / false / null 直接写就是固定值
  name: name_en                   加 _zh / _en（或 中文姓名 / 英文姓名）固定语言
  profile:                        嵌套文档：往下缩进继续写字段
    city: city
`, `
== Cheat sheet ==
  age: 18..60                     integer range
  price: 0..99.90                 decimal range; decimal places are kept
  createdAt: 2023-01-01..now      time range; today, -30d, -6mo, -1y, +7d also work
  level: [normal 80%, vip 20%]    pick from a list, optional weights
  source: [app]                   constant string
  version: 2                      numbers / true / false / null are constants
  name: name_en                   add _zh / _en to pin the language
  profile:                        sub-document: indent and keep listing fields
    city: city
`))
		},
	}
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}

// ---------------------------------------------------------------------------
// init
// ---------------------------------------------------------------------------

func newInit() *cobra.Command {
	var force bool
	var locale string
	cmd := &cobra.Command{
		Use:   "init [file]",
		Short: i18n.T("生成一个带注释的示例配置（默认 mgen.yaml）", "create a commented example config (default mgen.yaml)"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "mgen.yaml"
			if len(args) == 1 {
				path = args[0]
			}
			lang := "en"
			if i18n.Zh {
				lang = "zh"
			}
			if locale != "" {
				lang = locale
			}
			if lang != "zh" && lang != "en" {
				return i18n.Errorf("--locale 只能是 zh 或 en", "--locale must be zh or en")
			}
			if _, err := os.Stat(path); err == nil && !force {
				return i18n.Errorf("%s 已存在，加 --force 覆盖", "%s already exists, use --force to overwrite", path)
			}
			b, _ := templates.ReadFile("templates/init." + lang + ".yaml")
			if err := os.WriteFile(path, b, 0o644); err != nil {
				return err
			}
			fmt.Println(i18n.Tf("已生成 %[1]s\n\n下一步：\n  mgen preview %[1]s    先看看生成的数据\n  mgen %[1]s            写入 MongoDB",
				"created %[1]s\n\nnext:\n  mgen preview %[1]s    see what the data looks like\n  mgen %[1]s            write it to MongoDB", path))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, i18n.T("覆盖已存在的文件", "overwrite an existing file"))
	cmd.Flags().StringVar(&locale, "locale", "", i18n.T("示例的语言：zh | en（默认跟随系统）", "example language: zh | en (default: system language)"))
	return cmd
}
