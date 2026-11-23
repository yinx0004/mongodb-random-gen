package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yinx0004/mongodb-random-gen/internal/gen"
	"github.com/yinx0004/mongodb-random-gen/internal/i18n"
)

func init() { i18n.Zh = true }

func TestParse(t *testing.T) {
	cfg, err := Parse([]byte(`
db: shop
users:
  count: 10_000
  drop: true
  fields:
    name: name
orders:
  locale: en
  fields:
    a: 1..2
locale: mixed
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB != "shop" || len(cfg.Collections) != 2 {
		t.Fatalf("bad config: %+v", cfg)
	}
	u, o := cfg.Collections[0], cfg.Collections[1]
	if u.Name != "users" || u.Count != 10000 || !u.Drop || u.Locale != gen.ModeMixed {
		t.Fatalf("users wrong: %+v", u)
	}
	if o.Count != DefaultCount || o.Locale != gen.ModeEn {
		t.Fatalf("orders wrong: %+v", o)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"":      "空的",
		"db: x": "没有找到任何集合",
		"users:\n  cuont: 1\n  fields: {a: name}":          "count",
		"users:\n  count: 1\n  name: name":                 "fields 下面",
		"users:\n  count: 1":                               "缺少 fields",
		"users:\n  count: abc\n  fields: {a: name}":        "非负整数",
		"locale: jp\nu:\n  fields: {a: name}":              "zh、en 或 mixed",
		"users:\n\tcount: 1":                               "Tab",
		"dbb: x\nu:\n  fields: {a: name}":                  "db",
		"u:\n  fields: {a: name}\nu:\n  fields: {a: name}": "两次",
	}
	for src, want := range cases {
		_, err := Parse([]byte(src))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want mention of %q", src, err, want)
		}
	}
}

// The shipped init templates must parse and compile.
func TestInitTemplates(t *testing.T) {
	files, _ := filepath.Glob("../../cmd/mgen/templates/*.yaml")
	files2, _ := filepath.Glob("../../examples/*.yaml")
	files = append(files, files2...)
	if len(files) < 2 {
		t.Fatal("templates not found")
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		cfg, err := Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, c := range cfg.Collections {
			d, err := gen.CompileDoc(c.Fields, c.Name, c.Locale)
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			d.Stream(1, 0).Next()
		}
	}
}
