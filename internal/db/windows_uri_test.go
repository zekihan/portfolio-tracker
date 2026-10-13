package db

import (
	"net/url"
	"strings"
	"testing"
)

func TestSQLiteDSNCanonicalizesWindowsFileURI(t *testing.T) {
	t.Parallel()

	dsn, err := sqliteDSN(
		`file:C:\Users\Portfolio%20Owner\portfolio%23%2520.db?mode=memory&cache=shared`,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(dsn, "file:///C:/Users/Portfolio%20Owner/portfolio%23%2520.db?") {
		t.Fatalf("Windows URI = %q", dsn)
	}

	uri, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}

	if uri.Host != "" || uri.Opaque != "" || uri.Query().Get("mode") != "memory" ||
		uri.Query().Get("cache") != "shared" {
		t.Fatalf("changed URI semantics: %+v", uri)
	}
}

func TestSQLitePathURLPreservesNativeFilenames(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		path, want string
		windows    bool
	}{
		{
			path: `C:\Users\Portfolio Owner\data?#%20.db`,
			want: "file:///C:/Users/Portfolio%20Owner/data%3F%23%2520.db", windows: true,
		},
		{path: `C:/Users/Portfolio Owner/data.db`, want: "file:///C:/Users/Portfolio%20Owner/data.db", windows: true},
		{path: `\\server\share\data #%.db`, want: "file:////server/share/data%20%23%25.db", windows: true},
		{path: `\\?\C:\Users\data.db`, want: "file:///C:/Users/data.db", windows: true},
		{path: `\\?\UNC\server\share\data.db`, want: "file:////server/share/data.db", windows: true},
		{path: `/tmp/data\?#%20.db`, want: "file:///tmp/data%5C%3F%23%2520.db", windows: false},
	} {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()

			uri := sqlitePathURL(test.path, test.windows)
			if got := uri.String(); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}

			parsed, err := url.Parse(uri.String())
			if err != nil || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
				t.Fatalf("filename leaked into URI components: %+v, %v", parsed, err)
			}
		})
	}
}
