package gen

import (
	"fmt"
	"math/rand/v2"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Example generates one sample value of t, for help output.
func Example(t *Type, loc Locale, r *rand.Rand) string {
	fn := t.Fn
	if t.Build != nil {
		var err error
		if fn, err = t.Build(nil); err != nil {
			return ""
		}
	}
	return Format(fn(&Ctx{R: r, Loc: loc}, loc))
}

// Format renders a generated value compactly for humans.
func Format(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case time.Time:
		return x.Format("2006-01-02T15:04:05Z")
	case bson.ObjectID:
		return "ObjectId(" + x.Hex() + ")"
	case bson.D, bson.A:
		b, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: x}}, false, false)
		if err != nil {
			return fmt.Sprint(x)
		}
		s := string(b)
		return s[len(`{"v":`) : len(s)-1]
	default:
		return fmt.Sprint(x)
	}
}
