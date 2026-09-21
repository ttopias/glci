package expr

import "testing"

func TestEval(t *testing.T) {
	vars := map[string]string{
		"CI_COMMIT_BRANCH":   "main",
		"CI_PIPELINE_SOURCE": "push",
		"CI_COMMIT_TAG":      "",
		"CI_COMMIT_MESSAGE":  "feat: hello",
	}
	cases := []struct {
		src  string
		want bool
	}{
		{`$CI_COMMIT_BRANCH == "main"`, true},
		{`$CI_COMMIT_BRANCH == "dev"`, false},
		{`$CI_COMMIT_BRANCH != "dev"`, true},
		{`$CI_PIPELINE_SOURCE == "push" && $CI_COMMIT_BRANCH`, true},
		{`$CI_COMMIT_TAG`, false},
		{`$CI_COMMIT_MESSAGE =~ /feat:/`, true},
		{`$CI_COMMIT_MESSAGE !~ /feat:/`, false},
		{`$CI_COMMIT_BRANCH == "main" || $CI_COMMIT_TAG`, true},
		{`($CI_COMMIT_BRANCH == "dev")`, false},
		{`!$CI_COMMIT_TAG`, true},
		{`$CI_COMMIT_TAG == null`, true},
		{`$CI_COMMIT_BRANCH == null`, false},
		{"", false},
	}
	for _, tc := range cases {
		got, err := Eval(tc.src, vars)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v want %v", tc.src, got, tc.want)
		}
	}
	if _, err := Eval(`$CI_COMMIT_BRANCH == "main" leftover`, vars); err == nil {
		t.Fatal("expected unexpected token")
	}
}
