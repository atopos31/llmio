package service

import "testing"

func TestSplitProviderErrorMatchers(t *testing.T) {
	got := splitProviderErrorMatchers("\"status\":\"439\";\n\"status\": \"500\"；API Token has expired")
	want := []string{`"status":"439"`, `"status": "500"`, "API Token has expired"}

	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, want %d, got=%v", len(got), len(want), got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestMatchProviderBodyError(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		matchers    string
		wantMatched bool
	}{
		{
			name:        "json matcher with different spaces",
			body:        `{"status":"439","msg":"token expired"}`,
			matchers:    `"status": "439";"status": "500"`,
			wantMatched: true,
		},
		{
			name:        "plain text matcher",
			body:        `{"msg":"Your API Token has expired."}`,
			matchers:    `API Token has expired`,
			wantMatched: true,
		},
		{
			name:        "no matcher config",
			body:        `{"status":"439"}`,
			matchers:    ``,
			wantMatched: false,
		},
		{
			name:        "not matched",
			body:        `{"status":"200","msg":"ok"}`,
			matchers:    `"status":"439";token expired`,
			wantMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, _ := matchProviderBodyError(tt.body, tt.matchers)
			if matched != tt.wantMatched {
				t.Fatalf("matched=%v, want %v", matched, tt.wantMatched)
			}
		})
	}
}
