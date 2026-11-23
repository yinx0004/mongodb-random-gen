// Package config loads an mgen YAML file.
//
//	uri: mongodb://localhost:27017   # optional
//	db: shop                         # optional, default "test"
//	locale: zh                       # optional: zh | en | mixed
//	users:                           # every other top-level key is a collection
//	  count: 1000
//	  drop: true                     # optional
//	  locale: en                     # optional, overrides the top-level one
//	  fields:
//	    name: name
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/yinx0004/mongodb-random-gen/internal/gen"
	"github.com/yinx0004/mongodb-random-gen/internal/i18n"
)

const DefaultCount = 1000

type Config struct {
	URI         string
	DB          string
	Locale      gen.LocaleMode
	Collections []*Collection
}

type Collection struct {
	Name      string
	Count     int
	Drop      bool
	Locale    gen.LocaleMode
	HasLocale bool
	Fields    *yaml.Node
}

var topKeys = []string{"uri", "db", "locale"}
var collKeys = []string{"count", "fields", "drop", "locale"}

// Load reads and validates a config file (structure only; fields are compiled later).
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, i18n.Errorf("读取配置文件失败：%v", "cannot read config: %v", err)
	}
	return Parse(raw)
}

func Parse(raw []byte) (*Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "cannot start any token") || strings.Contains(msg, "tab") {
			msg += i18n.T("（可能用了 Tab 缩进，YAML 只能用空格缩进）", " (YAML must be indented with spaces, not tabs)")
		}
		return nil, i18n.Errorf("YAML 格式错误：%s", "invalid YAML: %s", msg)
	}
	if len(root.Content) == 0 {
		return nil, i18n.Errorf("配置文件是空的。可以运行 mgen init 生成一个示例", "the config is empty. Run `mgen init` to create an example")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, lineErr(doc, i18n.T("顶层应该是“集合名: 配置”的形式", "top level must map collection names to their settings"))
	}

	cfg := &Config{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		k, v := doc.Content[i], doc.Content[i+1]
		switch k.Value {
		case "uri":
			cfg.URI = v.Value
		case "db":
			cfg.DB = v.Value
		case "locale":
			m, ok := gen.ParseLocaleMode(v.Value)
			if !ok {
				return nil, badLocale(v)
			}
			cfg.Locale = m
		default:
			c, err := parseCollection(k, v)
			if err != nil {
				return nil, err
			}
			for _, prev := range cfg.Collections {
				if prev.Name == c.Name {
					return nil, lineErr(k, i18n.Tf("集合 %q 写了两次", "collection %q is defined twice", c.Name))
				}
			}
			cfg.Collections = append(cfg.Collections, c)
		}
	}
	if len(cfg.Collections) == 0 {
		return nil, i18n.Errorf("没有找到任何集合。顶层除了 %s 以外的键都是集合名，例如：\n\nusers:\n  count: 100\n  fields:\n    name: name",
			"no collections found. Every top-level key other than %s is a collection, e.g.:\n\nusers:\n  count: 100\n  fields:\n    name: name",
			strings.Join(topKeys, "/"))
	}
	for _, c := range cfg.Collections {
		if !c.HasLocale {
			c.Locale = cfg.Locale
		}
	}
	return cfg, nil
}

func parseCollection(k, v *yaml.Node) (*Collection, error) {
	name := k.Value
	if v.Kind != yaml.MappingNode {
		if s := suggestKey(name, topKeys); s != "" {
			return nil, lineErr(k, i18n.Tf("未知配置项 %q，是不是想写 %s？", "unknown setting %q, did you mean %s?", name, s))
		}
		return nil, lineErr(k, i18n.Tf("集合 %s 下面应该写 count 和 fields", "collection %s needs `count` and `fields` below it", name))
	}
	c := &Collection{Name: name, Count: DefaultCount}
	for i := 0; i+1 < len(v.Content); i += 2 {
		ck, cv := v.Content[i], v.Content[i+1]
		switch ck.Value {
		case "count":
			n, err := strconv.Atoi(strings.ReplaceAll(cv.Value, "_", ""))
			if err != nil || n < 0 {
				return nil, lineErr(cv, i18n.Tf("%s.count 应该是非负整数，现在是 %q", "%s.count must be a non-negative integer, got %q", name, cv.Value))
			}
			c.Count = n
		case "drop":
			b, err := strconv.ParseBool(cv.Value)
			if err != nil {
				return nil, lineErr(cv, i18n.Tf("%s.drop 应该是 true 或 false", "%s.drop must be true or false", name))
			}
			c.Drop = b
		case "locale":
			m, ok := gen.ParseLocaleMode(cv.Value)
			if !ok {
				return nil, badLocale(cv)
			}
			c.Locale, c.HasLocale = m, true
		case "fields":
			c.Fields = cv
		default:
			msg := i18n.T(fmt.Sprintf("集合 %s 下有未知配置项 %q", name, ck.Value),
				fmt.Sprintf("unknown setting %q in collection %s", ck.Value, name))
			if s := suggestKey(ck.Value, collKeys); s != "" {
				msg += i18n.Tf("，是不是想写 %s？", ", did you mean %s?", s)
			} else {
				msg += i18n.T("。字段请写在 fields 下面", ". Fields go under `fields:`")
			}
			return nil, lineErr(ck, msg)
		}
	}
	if c.Fields == nil {
		return nil, lineErr(k, i18n.Tf("集合 %s 缺少 fields（要生成的字段写在 fields 下面）", "collection %s has no `fields` (list the fields to generate under it)", name))
	}
	return c, nil
}

func badLocale(n *yaml.Node) error {
	return lineErr(n, i18n.Tf("locale 只能是 zh、en 或 mixed，现在是 %q", "locale must be zh, en or mixed, got %q", n.Value))
}

func lineErr(n *yaml.Node, msg string) error {
	return i18n.Errorf("第 %d 行：%s", "line %d: %s", n.Line, msg)
}

func suggestKey(s string, keys []string) string {
	s = strings.ToLower(s)
	for _, k := range keys {
		if s == k {
			continue
		}
		if (len(s) >= 3 && (strings.HasPrefix(k, s) || strings.HasPrefix(s, k))) || editDistance1(s, k) {
			return k
		}
	}
	return ""
}

// editDistance1 reports whether a and b differ by at most one edit or one adjacent swap.
func editDistance1(a, b string) bool {
	if a == b {
		return true
	}
	la, lb := len(a), len(b)
	if la-lb > 1 || lb-la > 1 {
		return false
	}
	i := 0
	for i < la && i < lb && a[i] == b[i] {
		i++
	}
	switch {
	case la == lb:
		if i+1 < la && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:] {
			return true
		}
		return a[i+1:] == b[i+1:]
	case la > lb:
		return a[i+1:] == b[i:]
	default:
		return a[i:] == b[i+1:]
	}
}
