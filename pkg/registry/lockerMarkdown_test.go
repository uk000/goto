package registry

import "testing"

func TestRenderLockersMarkdown(t *testing.T) {
	input := map[string]any{
		"l1": map[string]any{
			"name":  map[string]any{"data": "foo"},
			"items": map[string]any{"data": `["a","b"]`, "firstReported": "2026-10-08T10:00:00Z", "lastReported": "2026-10-08T10:01:00Z"},
			"cfg": map[string]any{
				"data": `{"x":1,"y":{"z":"q"}}`,
				"sub":  map[string]any{"data": "v"},
			},
		},
	}
	expected := "## l1\n" +
		"**cfg**\n" +
		"- x:  \n" +
		"  1\n" +
		"- y\n" +
		"  - z:  \n" +
		"    q\n" +
		"- sub:  \n" +
		"  v\n" +
		"\n" +
		"**items**\n" +
		"- a\n" +
		"- b\n" +
		"\n" +
		"**name**:  \n" +
		"foo\n"
	if got := renderLockersMarkdown(input); got != expected {
		t.Errorf("unexpected markdown:\n%q\nexpected:\n%q", got, expected)
	}
}

func TestRenderLockersMarkdownNilDataAndTrimKey(t *testing.T) {
	input := map[string]any{
		"goto,1,3": map[string]any{
			"A1": map[string]any{
				"B1": map[string]any{
					"...":  "",
					"data": "goto,1,3\nA1,B1\n",
				},
				"C1": map[string]any{
					"data": "goto,1,3\nA1,C1\n",
				},
				"data":          nil,
				"firstReported": "2026-10-09T15:16:06.855953-07:00",
				"lastReported":  "2026-10-09T15:16:06.905097-07:00",
			},
		},
	}
	expected := "## goto,1,3\n" +
		"**A1**\n" +
		"- B1\n" +
		"  - goto,1,3  \n" +
		"    A1,B1\n" +
		"  - ...\n" +
		"- C1:  \n" +
		"  goto,1,3  \n" +
		"  A1,C1\n"
	if got := renderLockersMarkdown(input); got != expected {
		t.Errorf("unexpected markdown:\n%q\nexpected:\n%q", got, expected)
	}
}
