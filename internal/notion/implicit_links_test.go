package notion

import "testing"

func TestImplicitFileAndDomainLinksAreEquivalentOnlyToTheirOwnTarget(t *testing.T) {
	for _, tc := range []struct{ plain, actual string }{
		{"检查 cache.rs", "检查 [cache.rs](http://cache.rs)"},
		{"运行 setup.sh", "运行 [setup.sh](http://setup.sh)"},
		{"查看 example.com", "查看 [example.com](http://example.com)"},
		{"查看 example.com", "查看 [example.com](https://example.com)"},
	} {
		if canonicalMarkdown(tc.plain) != canonicalMarkdown(tc.actual) {
			t.Errorf("implicit native link changed text: %q", tc.actual)
		}
	}
	if canonicalMarkdown("cache.rs") == canonicalMarkdown("[cache.rs](https://different.invalid)") {
		t.Fatal("a different target was silently discarded")
	}
	if canonicalMarkdown("较少的内容") == canonicalMarkdown("较少的内容，以及新的事实") {
		t.Fatal("real content differences were ignored")
	}
}
