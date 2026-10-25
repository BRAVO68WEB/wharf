package repopath

import "testing"

func TestName_keepsOperationWordsInsideTheRepository(t *testing.T) {
	cases := []struct {
		path string
		name string
	}{
		{"/v2/team/blobs/app/blobs/sha256:abcd", "team/blobs/app"},
		{"/team/blobs/app/blobs/sha256:abcd", "team/blobs/app"},
		{"/v2/app/manifests/latest", "app"},
		{"/v2/app/tags/list", "app"},
		{"/v2/app/referrers/sha256:abcd", "app"},
	}
	for _, tc := range cases {
		got, ok := Name(tc.path)
		if !ok || got != tc.name {
			t.Errorf("Name(%q) = %q, %v; want %q", tc.path, got, ok, tc.name)
		}
	}
	if _, ok := Name("/v2/_catalog"); ok {
		t.Fatal("catalog was treated as a repository")
	}
	if _, ok := Name("/v2/"); ok {
		t.Fatal("version check was treated as a repository")
	}
}
