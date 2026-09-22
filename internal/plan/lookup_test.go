package plan

import "testing"

func TestDefinitionSymbolsFromSequentialLocate(t *testing.T) {
	task := "ファイルを変更せず、RewriteWith、extractTools、applyCompactToMessages、DefaultOptions、DefaultUpstream の定義を調べてください。検索して読んで、並列化せず順に確認してください。"
	got := DefinitionSymbols(task)
	want := []string{"RewriteWith", "extractTools", "applyCompactToMessages", "DefaultOptions", "DefaultUpstream"}
	if len(got) != len(want) {
		t.Fatalf("symbols %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("symbols %v", got)
		}
	}
}

func TestDefinitionSymbolsSkipsOpenEndedWork(t *testing.T) {
	if got := DefinitionSymbols("fix the failing test in the proxy"); got != nil {
		t.Fatalf("open task symbols %v", got)
	}
	many := "定義を調べて NameAa、NameBb、NameCc、NameDd、NameEe、NameFf、NameGg、NameHh、NameIi"
	if got := DefinitionSymbols(many); got != nil {
		t.Fatalf("too many symbols %v", got)
	}
}
