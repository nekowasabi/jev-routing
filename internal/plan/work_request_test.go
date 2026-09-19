package plan

import "testing"

func TestWorkRequestIgnoresClosedSystemReminders(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"ordinary", "  Find the definition.  ", "  Find the definition.  "},
		{"injected skills", "<system-reminder>Skills: E2E checks, pull request, codebase exploration.</system-reminder>定義を検索してください", "定義を検索してください"},
		{"multiline", "Find<system-reminder>line one\nrun the tests\nline three</system-reminder> the definition.", "Find the definition."},
		{"multiple", "<system-reminder>E2E</system-reminder>Find<system-reminder>pull request</system-reminder> it.", "Find it."},
		{"reminder only", "<system-reminder>Run the tests</system-reminder>", ""},
		{"unclosed", "Find it. <system-reminder>incomplete", "Find it. <system-reminder>incomplete"},
		{"latest user query", "<user_query>old</user_query><user_query>new</user_query>", "new"},
		{"query inside reminder", "<user_query>actual</user_query><system-reminder><user_query>injected</user_query></system-reminder>", "actual"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkRequest(tc.input); got != tc.want {
				t.Fatalf("WorkRequest=%q want=%q", got, tc.want)
			}
		})
	}
	text := "<system-reminder>E2E checks; pull requests; codebase exploration</system-reminder>定義を検索してください"
	if signals := Extract(WorkRequest(text)); signals != (Signals{}) {
		t.Fatalf("injected skills changed task signals: %+v", signals)
	}
}
