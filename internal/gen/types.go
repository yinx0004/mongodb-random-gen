package gen

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/yinx0004/mongodb-random-gen/internal/data"
	"github.com/yinx0004/mongodb-random-gen/internal/i18n"
)

// LocalFunc produces one value for the given locale.
type LocalFunc func(c *Ctx, loc Locale) any

// Type is a named value generator, e.g. "name" or "phone".
type Type struct {
	Name      string
	Aliases   []string // other names, including Chinese ones
	Group     Group
	DescZh    string
	DescEn    string
	Usage     string // only for types that take arguments, e.g. "text 10..50"
	Localized bool   // output depends on zh / en

	// Fn is used for types without arguments; Build for types with arguments.
	Fn    LocalFunc
	Build func(args []string) (LocalFunc, error)
}

type Group int

const (
	GroupPerson Group = iota
	GroupAddress
	GroupWeb
	GroupText
	GroupOther
)

func (g Group) Title() string {
	switch g {
	case GroupPerson:
		return i18n.T("人物 / 联系方式", "Person / contact")
	case GroupAddress:
		return i18n.T("地址 / 位置", "Address / location")
	case GroupWeb:
		return i18n.T("公司 / 网络", "Company / internet")
	case GroupText:
		return i18n.T("文本", "Text")
	default:
		return i18n.T("ID / 时间 / 其他", "ID / time / misc")
	}
}

// Types is the registry, in display order.
var Types []*Type

var typeIndex = map[string]*Type{}

// Lookup finds a type by name or alias. Matching is exact (case-sensitive) so
// that literal values such as GeoJSON's "Point" are never mistaken for types.
func Lookup(name string) *Type { return typeIndex[name] }

func register(t *Type) {
	Types = append(Types, t)
	for _, n := range append([]string{t.Name}, t.Aliases...) {
		if _, dup := typeIndex[n]; dup {
			panic("duplicate type name " + n)
		}
		typeIndex[n] = t
	}
}

func pick[T any](r *rand.Rand, s []T) T { return s[r.IntN(len(s))] }

func between(r *rand.Rand, lo, hi int) int { return lo + r.IntN(hi-lo+1) }

func digits(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('0' + r.IntN(10))
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// People
// ---------------------------------------------------------------------------

var surnameTotal = func() float64 {
	s := 0.0
	for _, x := range data.ZhSurnames {
		s += x.Weight
	}
	return s
}()

func zhSurname(r *rand.Rand) data.Surname {
	x := r.Float64() * surnameTotal
	for _, s := range data.ZhSurnames {
		if x < s.Weight {
			return s
		}
		x -= s.Weight
	}
	return data.ZhSurnames[0]
}

// zhGiven returns a 1-2 character given name and its pinyin.
func zhGiven(r *rand.Rand) (string, string) {
	a := pick(r, data.ZhGivenChars)
	if r.IntN(10) < 3 {
		return a.Hanzi, a.Pinyin
	}
	b := pick(r, data.ZhGivenChars)
	for b.Hanzi == a.Hanzi {
		b = pick(r, data.ZhGivenChars)
	}
	return a.Hanzi + b.Hanzi, a.Pinyin + b.Pinyin
}

func username(c *Ctx, loc Locale) string {
	r := c.R
	var first, last string
	if loc == Zh {
		last = zhSurname(r).Pinyin
		_, first = zhGiven(r)
	} else {
		first = strings.ToLower(pick(r, data.EnFirstNames))
		last = strings.ToLower(pick(r, data.EnLastNames))
	}
	var base string
	switch r.IntN(5) {
	case 0:
		base = first + "." + last
	case 1:
		base = first + "_" + last
	case 2:
		base = first[:1] + last
	default:
		if loc == Zh {
			base = last + first
		} else {
			base = first + last
		}
	}
	switch r.IntN(3) {
	case 0:
		return base
	case 1:
		return base + fmt.Sprint(between(r, 1970, 2008))
	default:
		return base + fmt.Sprint(between(r, 1, 999))
	}
}

// idCard builds an 18-digit Chinese ID number with a valid check digit.
func idCard(r *rand.Rand) string {
	start := time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)
	birth := start.AddDate(0, 0, r.IntN(46*365))
	body := pick(r, data.ZhIDRegions) + birth.Format("20060102") + digits(r, 3)
	weights := []int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	sum := 0
	for i, w := range weights {
		sum += int(body[i]-'0') * w
	}
	return body + string("10X98765432"[sum%11])
}

// ---------------------------------------------------------------------------
// Address
// ---------------------------------------------------------------------------

type place struct {
	prov *data.Province
	city *data.City
}

func randomPlace(r *rand.Rand, loc Locale) place {
	provs := data.ZhProvinces
	if loc == En {
		provs = data.EnStates
	}
	p := &provs[r.IntN(len(provs))]
	return place{p, &p.Cities[r.IntN(len(p.Cities))]}
}

func zipcode(r *rand.Rand, loc Locale, city *data.City) string {
	if loc == Zh {
		return city.Zip[:4] + digits(r, 2)
	}
	return city.Zip + digits(r, 2)
}

func street(r *rand.Rand, loc Locale) string {
	if loc == Zh {
		return pick(r, data.ZhStreets)
	}
	return pick(r, data.EnStreetNames) + " " + pick(r, data.EnStreetSuffixes)
}

func address(c *Ctx, loc Locale) any {
	r := c.R
	pl := randomPlace(r, loc)
	if loc == Zh {
		region := pl.prov.Name + pl.city.Name
		if pl.prov.Name == pl.city.Name { // 直辖市
			region = pl.city.Name
		}
		s := fmt.Sprintf("%s%s%s%d号", region, pick(r, pl.city.Districts), street(r, loc), between(r, 1, 999))
		if r.IntN(2) == 0 {
			s += fmt.Sprintf("%d栋%d%02d室", between(r, 1, 30), between(r, 1, 32), between(r, 1, 6))
		}
		return s
	}
	s := fmt.Sprintf("%d %s", between(r, 1, 9999), street(r, loc))
	if r.IntN(4) == 0 {
		s += fmt.Sprintf(" Apt %d", between(r, 1, 999))
	}
	return fmt.Sprintf("%s, %s, %s %s", s, pl.city.Name, pl.prov.Abbr, zipcode(r, loc, pl.city))
}

// ---------------------------------------------------------------------------
// Text
// ---------------------------------------------------------------------------

func zhSentence(r *rand.Rand, n int) string {
	if n < 2 {
		n = 2
	}
	// Only cut between words, so the result has n-1 or n-2 characters before "。".
	var b strings.Builder
	cnt, last := 0, ""
	for cnt < n-2 {
		w := pick(r, data.ZhWords)
		if w == last {
			continue
		}
		if cnt > 3 && r.IntN(7) == 0 && cnt+3 < n-1 && last != "，" {
			w = "，"
		}
		wl := utf8.RuneCountInString(w)
		if cnt+wl > n-1 {
			if cnt == 0 { // a single word longer than the limit
				w = string([]rune(w)[:n-1])
				wl = n - 1
			} else {
				break
			}
		}
		b.WriteString(w)
		cnt += wl
		last = w
	}
	s := strings.TrimSuffix(b.String(), "，")
	return s + "。"
}

// enSentence builds a sentence of roughly lo..hi characters (never above hi).
func enSentence(r *rand.Rand, lo, hi int) string {
	target := between(r, lo, hi)
	var b strings.Builder
	for b.Len() < target-1 {
		w := pick(r, data.EnWords)
		if b.Len() > 0 {
			if b.Len()+1+len(w) > hi-1 {
				break
			}
			b.WriteByte(' ')
		} else if len(w) > hi-1 {
			w = w[:max(1, hi-1)]
		}
		b.WriteString(w)
	}
	s := b.String()
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func lengthArgs(name string, args []string, defLo, defHi int) (int, int, error) {
	if len(args) == 0 {
		return defLo, defHi, nil
	}
	lo, hi, ok := parseIntRange(args[0])
	if len(args) > 1 || !ok || lo < 1 {
		return 0, 0, i18n.Errorf("%s 的长度要写成范围，如 %s 10..50", "length for %s must be a range, e.g. %s 10..50", name, name)
	}
	return lo, hi, nil
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

func init() {
	// People
	register(&Type{Name: "name", Aliases: []string{"姓名", "名字", "full_name"}, Group: GroupPerson, Localized: true,
		DescZh: "姓名", DescEn: "full name",
		Fn: func(c *Ctx, loc Locale) any {
			if loc == Zh {
				g, _ := zhGiven(c.R)
				return zhSurname(c.R).Hanzi + g
			}
			return pick(c.R, data.EnFirstNames) + " " + pick(c.R, data.EnLastNames)
		}})
	register(&Type{Name: "first_name", Aliases: []string{"名", "firstname"}, Group: GroupPerson, Localized: true,
		DescZh: "名", DescEn: "first name",
		Fn: func(c *Ctx, loc Locale) any {
			if loc == Zh {
				g, _ := zhGiven(c.R)
				return g
			}
			return pick(c.R, data.EnFirstNames)
		}})
	register(&Type{Name: "last_name", Aliases: []string{"姓", "姓氏", "lastname", "surname"}, Group: GroupPerson, Localized: true,
		DescZh: "姓", DescEn: "last name",
		Fn: func(c *Ctx, loc Locale) any {
			if loc == Zh {
				return zhSurname(c.R).Hanzi
			}
			return pick(c.R, data.EnLastNames)
		}})
	register(&Type{Name: "gender", Aliases: []string{"性别", "sex"}, Group: GroupPerson, Localized: true,
		DescZh: "性别", DescEn: "gender",
		Fn: func(c *Ctx, loc Locale) any {
			if loc == Zh {
				return pick(c.R, []string{"男", "女"})
			}
			return pick(c.R, []string{"male", "female"})
		}})
	register(&Type{Name: "username", Aliases: []string{"用户名", "login"}, Group: GroupPerson, Localized: true,
		DescZh: "用户名（中文环境用拼音）", DescEn: "username",
		Fn: func(c *Ctx, loc Locale) any { return username(c, loc) }})
	register(&Type{Name: "email", Aliases: []string{"邮箱", "mail", "电子邮件"}, Group: GroupPerson, Localized: true,
		DescZh: "邮箱", DescEn: "email address",
		Fn: func(c *Ctx, loc Locale) any {
			if loc == Zh {
				if c.R.IntN(4) == 0 {
					return fmt.Sprintf("%d@qq.com", between(c.R, 10000000, 3999999999))
				}
				return username(c, loc) + "@" + pick(c.R, data.ZhEmailDomains)
			}
			return username(c, loc) + "@" + pick(c.R, data.EnEmailDomains)
		}})
	register(&Type{Name: "phone", Aliases: []string{"手机号", "手机", "电话", "mobile", "tel"}, Group: GroupPerson, Localized: true,
		DescZh: "手机号（中文为真实号段）", DescEn: "phone number",
		Fn: func(c *Ctx, loc Locale) any {
			if loc == Zh {
				return pick(c.R, data.ZhMobilePrefixes) + digits(c.R, 8)
			}
			pl := randomPlace(c.R, En)
			return fmt.Sprintf("+1-%s-%d-%s", pl.city.AreaCode, between(c.R, 200, 999), digits(c.R, 4))
		}})
	register(&Type{Name: "id_card", Aliases: []string{"身份证", "身份证号", "idcard"}, Group: GroupPerson,
		DescZh: "中国身份证号（校验位正确）", DescEn: "Chinese ID card number (valid checksum)",
		Fn: func(c *Ctx, _ Locale) any { return idCard(c.R) }})

	// Address
	register(&Type{Name: "address", Aliases: []string{"地址", "addr"}, Group: GroupAddress, Localized: true,
		DescZh: "完整地址", DescEn: "full address", Fn: address})
	register(&Type{Name: "province", Aliases: []string{"省份", "省", "state", "州"}, Group: GroupAddress, Localized: true,
		DescZh: "省份 / 州", DescEn: "province / state",
		Fn: func(c *Ctx, loc Locale) any { return randomPlace(c.R, loc).prov.Name }})
	register(&Type{Name: "city", Aliases: []string{"城市", "市"}, Group: GroupAddress, Localized: true,
		DescZh: "城市", DescEn: "city",
		Fn: func(c *Ctx, loc Locale) any { return randomPlace(c.R, loc).city.Name }})
	register(&Type{Name: "district", Aliases: []string{"区县", "区", "county"}, Group: GroupAddress, Localized: true,
		DescZh: "区县（英文为 county）", DescEn: "district / county",
		Fn: func(c *Ctx, loc Locale) any { return pick(c.R, randomPlace(c.R, loc).city.Districts) }})
	register(&Type{Name: "street", Aliases: []string{"街道", "路"}, Group: GroupAddress, Localized: true,
		DescZh: "街道", DescEn: "street",
		Fn: func(c *Ctx, loc Locale) any { return street(c.R, loc) }})
	register(&Type{Name: "zipcode", Aliases: []string{"邮编", "zip", "postcode"}, Group: GroupAddress, Localized: true,
		DescZh: "邮编", DescEn: "postal / ZIP code",
		Fn: func(c *Ctx, loc Locale) any { return zipcode(c.R, loc, randomPlace(c.R, loc).city) }})
	register(&Type{Name: "country", Aliases: []string{"国家"}, Group: GroupAddress, Localized: true,
		DescZh: "国家", DescEn: "country",
		Fn: func(c *Ctx, loc Locale) any {
			if loc == Zh {
				return pick(c.R, data.ZhCountries)
			}
			return pick(c.R, data.EnCountries)
		}})
	register(&Type{Name: "point", Aliases: []string{"坐标", "geo", "location"}, Group: GroupAddress, Localized: true,
		DescZh: "GeoJSON 坐标点（在真实城市附近，可建 2dsphere 索引）", DescEn: "GeoJSON point near a real city (2dsphere-ready)",
		Fn: func(c *Ctx, loc Locale) any {
			city := randomPlace(c.R, loc).city
			lng := round(city.Lng+(c.R.Float64()-0.5)*0.2, 6)
			lat := round(city.Lat+(c.R.Float64()-0.5)*0.2, 6)
			return bson.D{{Key: "type", Value: "Point"}, {Key: "coordinates", Value: bson.A{lng, lat}}}
		}})

	// Company / internet
	register(&Type{Name: "company", Aliases: []string{"公司"}, Group: GroupWeb, Localized: true,
		DescZh: "公司名", DescEn: "company name",
		Fn: func(c *Ctx, loc Locale) any {
			r := c.R
			if loc == Zh {
				city := strings.TrimSuffix(randomPlace(r, Zh).city.Name, "市")
				return city + pick(r, data.ZhCompanyWords) + pick(r, data.ZhCompanyTypes)
			}
			switch r.IntN(3) {
			case 0:
				return pick(r, data.EnLastNames) + " " + pick(r, data.EnCompanyIndustries) + " " + pick(r, data.EnCompanySuffixes)
			case 1:
				return pick(r, data.EnCompanyWords) + " " + pick(r, data.EnCompanyIndustries) + " " + pick(r, data.EnCompanySuffixes)
			default:
				return pick(r, data.EnLastNames) + " & " + pick(r, data.EnLastNames) + " " + pick(r, data.EnCompanySuffixes)
			}
		}})
	register(&Type{Name: "job", Aliases: []string{"职位", "岗位", "title"}, Group: GroupWeb, Localized: true,
		DescZh: "职位", DescEn: "job title",
		Fn: func(c *Ctx, loc Locale) any {
			if loc == Zh {
				return pick(c.R, data.ZhJobs)
			}
			return pick(c.R, data.EnJobs)
		}})
	domain := func(c *Ctx, loc Locale) string {
		tlds := []string{".com", ".io", ".net", ".org"}
		if loc == Zh {
			tlds = []string{".com", ".cn", ".com.cn", ".net"}
		}
		return pick(c.R, data.DomainWords) + pick(c.R, data.DomainWords) + pick(c.R, tlds)
	}
	register(&Type{Name: "domain", Aliases: []string{"域名"}, Group: GroupWeb, Localized: true,
		DescZh: "域名", DescEn: "domain name",
		Fn: func(c *Ctx, loc Locale) any { return domain(c, loc) }})
	register(&Type{Name: "url", Aliases: []string{"网址", "链接"}, Group: GroupWeb, Localized: true,
		DescZh: "网址", DescEn: "URL",
		Fn: func(c *Ctx, loc Locale) any {
			return "https://www." + domain(c, loc) + "/" + pick(c.R, data.DomainWords) + "/" + fmt.Sprint(between(c.R, 1, 99999))
		}})
	register(&Type{Name: "ip", Aliases: []string{"ipv4", "ip地址"}, Group: GroupWeb,
		DescZh: "IPv4 地址", DescEn: "IPv4 address",
		Fn: func(c *Ctx, _ Locale) any {
			first := between(c.R, 1, 223)
			if first == 127 {
				first = 128
			}
			return fmt.Sprintf("%d.%d.%d.%d", first, c.R.IntN(256), c.R.IntN(256), between(c.R, 1, 254))
		}})
	register(&Type{Name: "ipv6", Group: GroupWeb, DescZh: "IPv6 地址", DescEn: "IPv6 address",
		Fn: func(c *Ctx, _ Locale) any {
			parts := make([]string, 8)
			for i := range parts {
				parts[i] = fmt.Sprintf("%x", c.R.IntN(0x10000))
			}
			return strings.Join(parts, ":")
		}})
	register(&Type{Name: "mac", Aliases: []string{"mac地址"}, Group: GroupWeb, DescZh: "MAC 地址", DescEn: "MAC address",
		Fn: func(c *Ctx, _ Locale) any {
			parts := make([]string, 6)
			for i := range parts {
				parts[i] = fmt.Sprintf("%02x", c.R.IntN(256))
			}
			return strings.Join(parts, ":")
		}})
	register(&Type{Name: "user_agent", Aliases: []string{"ua", "useragent"}, Group: GroupWeb,
		DescZh: "浏览器 User-Agent", DescEn: "browser User-Agent",
		Fn: func(c *Ctx, _ Locale) any { return pick(c.R, data.UserAgents) }})

	// Text
	register(&Type{Name: "word", Aliases: []string{"词", "单词"}, Group: GroupText, Localized: true,
		DescZh: "一个词", DescEn: "one word",
		Fn: func(c *Ctx, loc Locale) any {
			if loc == Zh {
				return pick(c.R, data.ZhWords)
			}
			return pick(c.R, data.EnWords)
		}})
	register(&Type{Name: "text", Aliases: []string{"文本", "句子", "sentence"}, Group: GroupText, Localized: true,
		Usage: "text [10..50]", DescZh: "一句话，可指定字符数范围，默认 10..50", DescEn: "a sentence; optional length range, default 10..50",
		Build: func(args []string) (LocalFunc, error) {
			lo, hi, err := lengthArgs("text", args, 10, 50)
			if err != nil {
				return nil, err
			}
			return func(c *Ctx, loc Locale) any {
				if loc == Zh {
					return zhSentence(c.R, between(c.R, lo, hi))
				}
				return enSentence(c.R, lo, hi)
			}, nil
		}})
	register(&Type{Name: "paragraph", Aliases: []string{"段落"}, Group: GroupText, Localized: true,
		DescZh: "一段话（3~5 句）", DescEn: "a paragraph (3-5 sentences)",
		Fn: func(c *Ctx, loc Locale) any {
			n := between(c.R, 3, 5)
			parts := make([]string, n)
			for i := range parts {
				if loc == Zh {
					parts[i] = zhSentence(c.R, between(c.R, 12, 30))
				} else {
					parts[i] = enSentence(c.R, 30, 90)
				}
			}
			if loc == Zh {
				return strings.Join(parts, "")
			}
			return strings.Join(parts, " ")
		}})

	// ID / time / misc
	register(&Type{Name: "bool", Aliases: []string{"boolean", "布尔"}, Group: GroupOther,
		DescZh: "true / false", DescEn: "true / false",
		Fn: func(c *Ctx, _ Locale) any { return c.R.IntN(2) == 0 }})
	register(&Type{Name: "objectid", Aliases: []string{"oid", "object_id"}, Group: GroupOther,
		DescZh: "ObjectId", DescEn: "ObjectId",
		Fn: func(c *Ctx, _ Locale) any { return bson.NewObjectID() }})
	register(&Type{Name: "uuid", Group: GroupOther, DescZh: "UUID 字符串", DescEn: "UUID string",
		Fn: func(c *Ctx, _ Locale) any {
			var b [16]byte
			for i := range b {
				b[i] = byte(c.R.IntN(256))
			}
			b[6] = b[6]&0x0f | 0x40
			b[8] = b[8]&0x3f | 0x80
			return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
		}})
	register(&Type{Name: "date", Aliases: []string{"日期", "时间", "datetime"}, Group: GroupOther,
		DescZh: "近一年内的时间；指定范围请写 2023-01-01..now", DescEn: "a time within the last year; for a range write 2023-01-01..now",
		Fn: func(c *Ctx, _ Locale) any {
			now := time.Now().UTC()
			return now.Add(-time.Duration(c.R.Int64N(int64(365 * 24 * time.Hour)))).Truncate(time.Second)
		}})
	register(&Type{Name: "now", Aliases: []string{"当前时间"}, Group: GroupOther,
		DescZh: "生成时的当前时间", DescEn: "current time at generation",
		Fn: func(c *Ctx, _ Locale) any { return time.Now().UTC().Truncate(time.Millisecond) }})
	register(&Type{Name: "timestamp", Aliases: []string{"时间戳"}, Group: GroupOther,
		DescZh: "近一年内的 Unix 秒级时间戳（整数）", DescEn: "Unix timestamp in seconds within the last year",
		Fn: func(c *Ctx, _ Locale) any { return time.Now().Unix() - c.R.Int64N(365*24*3600) }})
	register(&Type{Name: "timestamp_ms", Aliases: []string{"毫秒时间戳"}, Group: GroupOther,
		DescZh: "近一年内的 Unix 毫秒时间戳（整数）", DescEn: "Unix timestamp in milliseconds within the last year",
		Fn: func(c *Ctx, _ Locale) any { return time.Now().UnixMilli() - c.R.Int64N(365*24*3600*1000) }})
}
