package gen

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"gopkg.in/yaml.v3"

	"github.com/yinx0004/mongodb-random-gen/internal/i18n"
)

func init() { i18n.Zh = true }

func fields(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content[0]
}

func doc(t *testing.T, src string, mode LocaleMode) *Stream {
	t.Helper()
	d, err := CompileDoc(fields(t, src), "c", mode)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return d.Stream(42, 0)
}

func compileErr(t *testing.T, src string) string {
	t.Helper()
	_, err := CompileDoc(fields(t, src), "c", ModeZh)
	if err == nil {
		t.Fatalf("expected an error for %q", src)
	}
	return err.Error()
}

func get(d bson.D, k string) any {
	for _, e := range d {
		if e.Key == k {
			return e.Value
		}
	}
	return nil
}

func TestRanges(t *testing.T) {
	d := doc(t, "i: 18..60\nn: -10..-5\nf: 0..10.00\ng: 1.5..2\nt: 2023-01-01..2023-01-31\nr: -30d..now\n", ModeZh)
	jan1 := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	for range 2000 {
		x := d.Next()
		if v := get(x, "i").(int); v < 18 || v > 60 {
			t.Fatalf("int out of range: %d", v)
		}
		if v := get(x, "n").(int); v < -10 || v > -5 {
			t.Fatalf("negative int out of range: %d", v)
		}
		f := get(x, "f").(float64)
		if f < 0 || f > 10 || round(f, 2) != f {
			t.Fatalf("float wrong: %v", f)
		}
		if g := get(x, "g").(float64); g < 1.5 || g > 2 || round(g, 1) != g {
			t.Fatalf("float 1 decimal wrong: %v", g)
		}
		tm := get(x, "t").(time.Time)
		if tm.Before(jan1) || tm.After(jan1.AddDate(0, 0, 30)) {
			t.Fatalf("date out of range: %v", tm)
		}
		r := get(x, "r").(time.Time)
		if time.Since(r) > 31*24*time.Hour || time.Since(r) < -time.Second {
			t.Fatalf("relative date out of range: %v", r)
		}
	}
}

func TestEnumWeights(t *testing.T) {
	d := doc(t, "a: [x 80%, y, z]\nb: [1, 2]\nc: [app]\n", ModeZh)
	cnt := map[any]int{}
	for range 20000 {
		x := d.Next()
		cnt[get(x, "a")]++
		if v := get(x, "b"); v != 1 && v != 2 {
			t.Fatalf("b should be typed int, got %#v", v)
		}
		if get(x, "c") != "app" {
			t.Fatal("constant list")
		}
	}
	if p := float64(cnt["x"]) / 20000; p < 0.77 || p > 0.83 {
		t.Fatalf("x share %.3f, want ~0.80", p)
	}
	if p := float64(cnt["y"]) / 20000; p < 0.08 || p > 0.12 {
		t.Fatalf("y share %.3f, want ~0.10", p)
	}
}

func TestConstantsNestedAndID(t *testing.T) {
	d := doc(t, "v: 2\nok: true\nn: null\nloc:\n  type: [Point]\n  coordinates: [1.5, 2]\np:\n  city: city\n", ModeZh)
	x := d.Next()
	if x[0].Key != "_id" {
		t.Fatal("_id should be added first")
	}
	if get(x, "v") != 2 || get(x, "ok") != true || get(x, "n") != nil {
		t.Fatalf("constants wrong: %v", x)
	}
	loc := get(x, "loc").(bson.D)
	if get(loc, "type") != "Point" {
		t.Fatalf("literal GeoJSON type broken: %v", loc)
	}
	if _, ok := get(x, "p").(bson.D); !ok {
		t.Fatal("nested doc should be bson.D")
	}

	d2 := doc(t, "_id: 1..9\na: name\n", ModeZh)
	if x := d2.Next(); len(x) != 2 || x[0].Key != "_id" {
		t.Fatalf("custom _id should be kept: %v", x)
	}
}

func TestLocales(t *testing.T) {
	cjk := regexp.MustCompile(`\p{Han}`)
	zh := doc(t, "n: name\np: phone\ne: name_en\nc: 中文城市\n", ModeZh)
	en := doc(t, "n: name\np: phone\nz: name_zh\n", ModeEn)
	phoneZh := regexp.MustCompile(`^1[3-9]\d{9}$`)
	phoneEn := regexp.MustCompile(`^\+1-\d{3}-\d{3}-\d{4}$`)
	for range 200 {
		x := zh.Next()
		if !cjk.MatchString(get(x, "n").(string)) || !phoneZh.MatchString(get(x, "p").(string)) {
			t.Fatalf("zh doc wrong: %v", x)
		}
		if cjk.MatchString(get(x, "e").(string)) || !cjk.MatchString(get(x, "c").(string)) {
			t.Fatalf("forced locale ignored: %v", x)
		}
		y := en.Next()
		if cjk.MatchString(get(y, "n").(string)) || !phoneEn.MatchString(get(y, "p").(string)) || !cjk.MatchString(get(y, "z").(string)) {
			t.Fatalf("en doc wrong: %v", y)
		}
	}
	// mixed: name and phone in one document share a locale
	mixed := doc(t, "n: name\np: phone\n", ModeMixed)
	seen := map[bool]bool{}
	for range 200 {
		x := mixed.Next()
		isZh := cjk.MatchString(get(x, "n").(string))
		if isZh != phoneZh.MatchString(get(x, "p").(string)) {
			t.Fatalf("mixed doc not coherent: %v", x)
		}
		seen[isZh] = true
	}
	if !seen[true] || !seen[false] {
		t.Fatal("mixed should produce both locales")
	}
}

func TestIDCardChecksum(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	weights := []int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	for range 1000 {
		id := idCard(r)
		if len(id) != 18 {
			t.Fatalf("bad length %q", id)
		}
		sum := 0
		for i, w := range weights {
			sum += int(id[i]-'0') * w
		}
		if id[17] != "10X98765432"[sum%11] {
			t.Fatalf("bad checksum %q", id)
		}
		if _, err := time.Parse("20060102", id[6:14]); err != nil {
			t.Fatalf("bad birth date in %q", id)
		}
	}
}

func TestTextLength(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for range 2000 {
		n := between(r, 3, 60)
		s := zhSentence(r, n)
		if l := utf8.RuneCountInString(s); l > n || !strings.HasSuffix(s, "。") || strings.Contains(s, "，。") {
			t.Fatalf("zh sentence %q (len %d, want <= %d)", s, l, n)
		}
		e := enSentence(r, 10, 50)
		if len(e) > 50 || !strings.HasSuffix(e, ".") {
			t.Fatalf("en sentence %q", e)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]string{
		"a: phnoe":                          "phone",
		"a: Point":                          "point",
		"a: int":                            "18..60",
		"a: hello world":                    "[hello]",
		"a: 18..abc":                        "无法识别的范围",
		"a: [x 60%, y 50%, z]":              "110%",
		"a: []":                             "列表是空的",
		"a: id_card_en":                     "不区分中英文",
		"a: name 3":                         "不需要参数",
		"a: text abc":                       "10..50",
		"a:\n  type: array\n  of: [x]":      "下个版本",
		"a:\n  type: email\n  unique: true": "下个版本",
		"a: 1\na: 2":                        "重复",
	}
	for src, want := range cases {
		if got := compileErr(t, src); !strings.Contains(got, want) {
			t.Errorf("%q: error %q should mention %q", src, got, want)
		}
	}
	if got := compileErr(t, "x: 1\ny: phnoe"); !strings.Contains(got, "第 2 行") || !strings.Contains(got, "c.y") {
		t.Errorf("error should carry line and path: %q", got)
	}
}

func TestAllTypesGenerate(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	for _, ty := range Types {
		for _, loc := range []Locale{Zh, En} {
			for range 50 {
				if Example(ty, loc, r) == "" {
					t.Fatalf("type %s produced empty example", ty.Name)
				}
			}
		}
	}
}
