// Package gen turns field specs written in YAML into value generators.
//
// Rules (see DESIGN.md):
//
//	18..60              integer range
//	0..99.90            float range, decimals copied from the endpoints
//	2023-01-01..now     date range (also -30d, -6mo, -1y, today ...)
//	[a, b 20%]          enum, optional weights
//	name / 姓名 ...      a named type, see Types
//	name_en / 英文姓名   a named type forced to one locale
//	nested map          sub-document
//	42 / true / null    constant
package gen

import (
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"gopkg.in/yaml.v3"

	"github.com/yinx0004/mongodb-random-gen/internal/i18n"
)

type Locale int

const (
	Zh Locale = iota
	En
)

// LocaleMode is the collection-level setting: zh, en or mixed.
type LocaleMode int

const (
	ModeZh LocaleMode = iota
	ModeEn
	ModeMixed
)

// ParseLocaleMode accepts zh/en/mixed and a few Chinese spellings.
func ParseLocaleMode(s string) (LocaleMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "zh", "cn", "zh_cn", "zh-cn", "中文":
		return ModeZh, true
	case "en", "en_us", "en-us", "英文":
		return ModeEn, true
	case "mixed", "mix", "混合":
		return ModeMixed, true
	}
	return 0, false
}

// Ctx is per-document generation state.
type Ctx struct {
	R   *rand.Rand
	Loc Locale // locale of the current document
}

// Func generates one value.
type Func func(c *Ctx) any

// Error is a config error tied to a YAML line and a field path.
type Error struct {
	Line int
	Path string
	Msg  string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return i18n.Tf("第 %d 行（%s）：%s", "line %d (%s): %s", e.Line, e.Path, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Msg)
}

func errAt(n *yaml.Node, path string, err error) *Error {
	return &Error{Line: n.Line, Path: path, Msg: err.Error()}
}

// Doc is the compiled template of one collection. It is read-only after
// compilation and safe to share between goroutines; each goroutine draws
// documents from its own Stream.
type Doc struct {
	mode   LocaleMode
	fields []field
}

type field struct {
	key string
	fn  Func
}

// CompileDoc compiles the `fields` mapping of a collection.
func CompileDoc(fields *yaml.Node, path string, mode LocaleMode) (*Doc, error) {
	fs, err := compileFields(fields, path)
	if err != nil {
		return nil, err
	}
	hasID := false
	for _, f := range fs {
		if f.key == "_id" {
			hasID = true
		}
	}
	if !hasID {
		fs = append([]field{{"_id", func(*Ctx) any { return bson.NewObjectID() }}}, fs...)
	}
	return &Doc{mode: mode, fields: fs}, nil
}

// Stream is a deterministic sequence of documents from one Doc. A Stream is
// not safe for concurrent use; give every goroutine its own.
type Stream struct {
	doc *Doc
	r   *rand.Rand
	ctx Ctx
}

// Stream returns a document stream seeded by (seed, part). The same pair
// always yields the same documents, apart from generated ObjectIds and `now`.
func (d *Doc) Stream(seed, part uint64) *Stream {
	r := rand.New(rand.NewPCG(seed, part^0x9e3779b97f4a7c15))
	return &Stream{doc: d, r: r, ctx: Ctx{R: r}}
}

// Next generates one document.
func (s *Stream) Next() bson.D {
	switch s.doc.mode {
	case ModeEn:
		s.ctx.Loc = En
	case ModeMixed:
		s.ctx.Loc = Locale(s.r.IntN(2))
	default:
		s.ctx.Loc = Zh
	}
	return buildDoc(&s.ctx, s.doc.fields)
}

func buildDoc(c *Ctx, fs []field) bson.D {
	out := make(bson.D, len(fs))
	for i, f := range fs {
		out[i] = bson.E{Key: f.key, Value: f.fn(c)}
	}
	return out
}

func compileFields(n *yaml.Node, path string) ([]field, error) {
	n = deref(n)
	if n.Kind != yaml.MappingNode {
		return nil, errAt(n, path, i18n.Errorf("这里应该是“字段名: 类型”的列表", "expected a mapping of `field: type`"))
	}
	seen := map[string]bool{}
	var fs []field
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		key := k.Value
		p := path + "." + key
		if seen[key] {
			return nil, errAt(k, p, i18n.Errorf("字段 %q 重复了", "duplicate field %q", key))
		}
		seen[key] = true
		fn, err := Compile(v, p)
		if err != nil {
			return nil, err
		}
		fs = append(fs, field{key, fn})
	}
	return fs, nil
}

func deref(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// Compile turns one field spec into a generator.
func Compile(n *yaml.Node, path string) (Func, error) {
	n = deref(n)
	switch n.Kind {
	case yaml.MappingNode:
		if isExpandedForm(n) {
			return nil, errAt(n, path, i18n.Errorf(
				"`type:` 展开写法（数组、缺失率、唯一值等）将在下个版本支持",
				"the expanded `type:` form (arrays, missing rate, unique ...) is coming in the next version"))
		}
		fs, err := compileFields(n, path)
		if err != nil {
			return nil, err
		}
		return func(c *Ctx) any { return buildDoc(c, fs) }, nil
	case yaml.SequenceNode:
		fn, err := compileEnum(n, path)
		if err != nil {
			return nil, errAt(n, path, err)
		}
		return fn, nil
	case yaml.ScalarNode:
		if n.Tag != "!!str" {
			v, err := scalarValue(n)
			if err != nil {
				return nil, errAt(n, path, err)
			}
			return func(*Ctx) any { return v }, nil
		}
		fn, err := compileString(n.Value)
		if err != nil {
			return nil, errAt(n, path, err)
		}
		return fn, nil
	}
	return nil, errAt(n, path, i18n.Errorf("无法识别的写法", "unrecognized value"))
}

// ---------------------------------------------------------------------------
// Strings: ranges and named types
// ---------------------------------------------------------------------------

func compileString(s string) (Func, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, i18n.Errorf("值是空的；想要固定的空字符串请写 [\"\"]", "empty value; for a constant empty string write [\"\"]")
	}
	tokens := strings.Fields(s)
	name, args := tokens[0], tokens[1:]
	t, forced, err := resolveType(name)
	if err != nil {
		if strings.Contains(s, "..") {
			return compileRange(s)
		}
		return nil, err
	}
	var fn LocalFunc
	if t.Build != nil {
		fn, err = t.Build(args)
		if err != nil {
			return nil, err
		}
	} else {
		if len(args) > 0 {
			return nil, i18n.Errorf("%s 不需要参数（多写了 %q）", "%s takes no arguments (got %q)", t.Name, strings.Join(args, " "))
		}
		fn = t.Fn
	}
	if forced != nil {
		loc := *forced
		return func(c *Ctx) any { return fn(c, loc) }, nil
	}
	return func(c *Ctx) any { return fn(c, c.Loc) }, nil
}

// resolveType finds a type, handling name_zh / name_en / 中文姓名 / 英文姓名.
func resolveType(name string) (*Type, *Locale, error) {
	if t := Lookup(name); t != nil {
		return t, nil, nil
	}
	for _, v := range []struct {
		base string
		loc  Locale
	}{
		{strings.TrimSuffix(name, "_zh"), Zh}, {strings.TrimSuffix(name, "_en"), En},
		{strings.TrimPrefix(name, "中文"), Zh}, {strings.TrimPrefix(name, "英文"), En},
	} {
		if v.base == name {
			continue
		}
		if t := Lookup(v.base); t != nil {
			if !t.Localized {
				return nil, nil, i18n.Errorf("%s 不区分中英文，直接写 %s 即可", "%s has no locale variants, just write %s", t.Name, t.Name)
			}
			loc := v.loc
			return t, &loc, nil
		}
	}
	return nil, nil, unknownType(name)
}

var hints = map[string][2]string{
	"int":      {"整数请直接写范围，如 18..60", "write integers as a range, e.g. 18..60"},
	"float":    {"小数请写范围，如 0..99.99（小数位数照抄）", "write decimals as a range, e.g. 0..99.99 (decimal places are kept)"},
	"enum":     {"枚举请写成列表，如 [a, b, c]", "write enums as a list, e.g. [a, b, c]"},
	"string":   {"随机文本用 text 或 word；固定字符串请写成 [xxx]", "use text or word for random text; for a constant string write [xxx]"},
	"upcoming": {"该写法将在下个版本支持", "this is coming in the next version"},
}

var hintAliases = map[string]string{
	"int": "int", "integer": "int", "number": "int", "long": "int", "整数": "int", "数字": "int", "年龄": "int",
	"float": "float", "double": "float", "decimal": "float", "小数": "float", "金额": "float", "价格": "float",
	"enum": "enum", "枚举": "enum", "choice": "enum",
	"string": "string", "str": "string", "字符串": "string",
	"seq": "upcoming", "sequence": "upcoming", "pattern": "upcoming", "ref": "upcoming", "array": "upcoming", "序号": "upcoming",
}

func unknownType(name string) error {
	if h, ok := hintAliases[strings.ToLower(name)]; ok {
		return i18n.Errorf("%q 不是类型名：%s", "%q is not a type: %s", name, i18n.T(hints[h][0], hints[h][1]))
	}
	msg := i18n.Tf("未知类型 %q", "unknown type %q", name)
	if s := suggest(name); s != "" {
		msg += i18n.Tf("，是不是想写 %s？", ", did you mean %s?", s)
	} else {
		msg += i18n.T("。运行 mgen types 查看全部类型", ". Run `mgen types` to see all types")
	}
	msg += i18n.Tf("（如果要的是固定值，请写成 [%s]）", " (for a constant value write [%s])", name)
	return fmt.Errorf("%s", msg)
}

func suggest(name string) string {
	best, bestD := "", 3
	lower := strings.ToLower(name)
	var names []string
	for k := range typeIndex {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		d := levenshtein(lower, k)
		limit := 2
		if utf8.RuneCountInString(k) <= 3 {
			limit = 1
		}
		if d <= limit && d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// ---------------------------------------------------------------------------
// Ranges
// ---------------------------------------------------------------------------

func parseIntRange(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, "..")
	if !ok {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		return n, n, err == nil
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(a))
	hi, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi, true
}

func decimals(s string) int {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}

func round(v float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(v*p) / p
}

func compileRange(s string) (Func, error) {
	a, b, _ := strings.Cut(s, "..")
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return nil, i18n.Errorf("范围两端都要写，如 18..60", "both ends of a range are required, e.g. 18..60")
	}
	// int
	if lo, err1 := strconv.ParseInt(a, 10, 64); err1 == nil {
		if hi, err2 := strconv.ParseInt(b, 10, 64); err2 == nil {
			if lo > hi {
				lo, hi = hi, lo
			}
			span := uint64(hi - lo)
			return func(c *Ctx) any {
				if span == math.MaxUint64 {
					return int(c.R.Uint64())
				}
				return int(lo + int64(c.R.Uint64N(span+1)))
			}, nil
		}
	}
	// float
	fa, err1 := strconv.ParseFloat(a, 64)
	fb, err2 := strconv.ParseFloat(b, 64)
	if err1 == nil && err2 == nil {
		if fa > fb {
			fa, fb = fb, fa
		}
		d := max(decimals(a), decimals(b))
		return func(c *Ctx) any {
			v := round(fa+c.R.Float64()*(fb-fa), d)
			return math.Min(math.Max(v, fa), fb)
		}, nil
	}
	// date
	now := time.Now().UTC().Truncate(time.Second)
	ta, err1 := parseTime(a, now)
	tb, err2 := parseTime(b, now)
	if err1 == nil && err2 == nil {
		if ta.After(tb) {
			ta, tb = tb, ta
		}
		span := tb.Sub(ta)
		return func(c *Ctx) any {
			if span <= 0 {
				return ta
			}
			return ta.Add(time.Duration(c.R.Int64N(int64(span)))).Truncate(time.Second)
		}, nil
	}
	return nil, i18n.Errorf("无法识别的范围 %q：两端应同为整数、小数或时间，如 18..60、0..99.9、2023-01-01..now、-30d..now",
		"unrecognized range %q: both ends must be integers, decimals or times, e.g. 18..60, 0..99.9, 2023-01-01..now, -30d..now", s)
}

var relTime = regexp.MustCompile(`^([+-]?\d+)(s|min|h|d|w|mo|y)$`)

// parseTime accepts now, today, 2024-01-01, 2024-01-01T08:00:00, RFC3339, and
// relative offsets like -30d, +2h, -6mo, -1y.
func parseTime(s string, now time.Time) (time.Time, error) {
	switch strings.ToLower(s) {
	case "now":
		return now, nil
	case "today":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), nil
	}
	if m := relTime.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "s":
			return now.Add(time.Duration(n) * time.Second), nil
		case "min":
			return now.Add(time.Duration(n) * time.Minute), nil
		case "h":
			return now.Add(time.Duration(n) * time.Hour), nil
		case "d":
			return now.AddDate(0, 0, n), nil
		case "w":
			return now.AddDate(0, 0, 7*n), nil
		case "mo":
			return now.AddDate(0, n, 0), nil
		case "y":
			return now.AddDate(n, 0, 0), nil
		}
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05", "2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", s)
}

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

var weighted = regexp.MustCompile(`^(.*?)\s+(\d+(?:\.\d+)?)%$`)

func compileEnum(n *yaml.Node, path string) (Func, error) {
	if len(n.Content) == 0 {
		return nil, i18n.Errorf("列表是空的，至少写一个候选值", "the list is empty; give at least one value")
	}
	vals := make([]any, len(n.Content))
	weights := make([]float64, len(n.Content))
	sum, unweighted := 0.0, 0
	for i, item := range n.Content {
		item = deref(item)
		if item.Kind == yaml.ScalarNode && item.Tag == "!!str" {
			if m := weighted.FindStringSubmatch(item.Value); m != nil {
				w, _ := strconv.ParseFloat(m[2], 64)
				vals[i] = resolvePlain(m[1])
				weights[i] = w
				sum += w
				continue
			}
		}
		v, err := nodeValue(item)
		if err != nil {
			return nil, err
		}
		vals[i] = v
		weights[i] = -1
		unweighted++
	}
	if unweighted == len(vals) {
		return func(c *Ctx) any { return vals[c.R.IntN(len(vals))] }, nil
	}
	if unweighted > 0 {
		rest := 100 - sum
		if rest <= 0 {
			return nil, i18n.Errorf("权重加起来已经是 %g%%，没写权重的选项分不到比例", "weights already add up to %g%%, nothing left for the unweighted values", sum)
		}
		for i := range weights {
			if weights[i] < 0 {
				weights[i] = rest / float64(unweighted)
			}
		}
		sum = 100
	}
	cum := make([]float64, len(weights))
	acc := 0.0
	for i, w := range weights {
		acc += w
		cum[i] = acc
	}
	return func(c *Ctx) any {
		x := c.R.Float64() * acc
		i := sort.SearchFloat64s(cum, x)
		if i >= len(vals) {
			i = len(vals) - 1
		}
		return vals[i]
	}, nil
}

// resolvePlain types a bare word the way YAML would: number, bool, null or string.
func resolvePlain(s string) any {
	s = strings.TrimSpace(s)
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err == nil && len(n.Content) == 1 && n.Content[0].Kind == yaml.ScalarNode {
		if v, err := scalarValue(n.Content[0]); err == nil {
			return v
		}
	}
	return s
}

func scalarValue(n *yaml.Node) (any, error) {
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	if t, ok := v.(time.Time); ok {
		return t.UTC(), nil
	}
	return v, nil
}

// nodeValue converts a YAML node to a constant BSON-friendly value (maps keep order).
func nodeValue(n *yaml.Node) (any, error) {
	n = deref(n)
	switch n.Kind {
	case yaml.MappingNode:
		d := bson.D{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := nodeValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			d = append(d, bson.E{Key: n.Content[i].Value, Value: v})
		}
		return d, nil
	case yaml.SequenceNode:
		a := bson.A{}
		for _, c := range n.Content {
			v, err := nodeValue(c)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		return a, nil
	default:
		return scalarValue(n)
	}
}

// ---------------------------------------------------------------------------
// Expanded form detection (implemented in the next milestone)
// ---------------------------------------------------------------------------

var expandedOptions = map[string]bool{
	"type": true, "len": true, "of": true, "unique": true, "missing": true, "null": true,
	"locale": true, "format": true, "to": true,
}

var upcomingTypes = map[string]bool{"array": true, "seq": true, "pattern": true, "ref": true}

func isExpandedForm(n *yaml.Node) bool {
	var typ *yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		if !expandedOptions[k] {
			return false
		}
		if k == "type" {
			typ = deref(n.Content[i+1])
		}
	}
	if typ == nil || typ.Kind != yaml.ScalarNode || strings.TrimSpace(typ.Value) == "" {
		return false
	}
	name := strings.Fields(typ.Value)[0]
	if upcomingTypes[name] {
		return true
	}
	_, _, err := resolveType(name)
	return err == nil
}
