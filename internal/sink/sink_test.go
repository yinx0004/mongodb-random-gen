package sink

import "testing"

func TestRedactURI(t *testing.T) {
	cases := map[string]string{
		"mongodb://user:pass@host:27017/db?connectTimeoutMS=2000":           "mongodb://user:***@host:27017/db?connectTimeoutMS=2000",
		"mongodb://user:p@ss/w0rd@h1:27017,h2:27017/?connectTimeoutMS=2000": "mongodb://user:***@h1:27017,h2:27017/?connectTimeoutMS=2000",
		"mongodb://user:pa:ss@host/":                                        "mongodb://user:***@host/",
		"mongodb://host:27017/?connectTimeoutMS=2000":                       "mongodb://host:27017/?connectTimeoutMS=2000",
		"mongodb+srv://u:p@cluster.example.net/db":                          "mongodb+srv://u:***@cluster.example.net/db",
		"not a uri": "not a uri",
	}
	for in, want := range cases {
		if got := RedactURI(in); got != want {
			t.Errorf("RedactURI(%q) = %q, want %q", in, got, want)
		}
	}
}
