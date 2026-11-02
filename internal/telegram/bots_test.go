package telegram

import "testing"

func TestBotToken(t *testing.T) {
	token := "1234567890:AAH9kBhZ_examplexampleexampleexample-x"
	id, err := botID(token)
	if err != nil || id != 1234567890 {
		t.Fatalf("id %d, %v", id, err)
	}
	for _, bad := range []string{"", "1234567890", "abc:AAH9kBhZ_examplexampleexampleexample", token + " ", "x" + token} {
		if _, err := botID(bad); err == nil {
			t.Fatalf("%q passed as a token", bad)
		}
	}
	reply := "Done! Congratulations on your new bot.\n\nUse this token to access the HTTP API:\n" + token + "\nKeep your token secure"
	if got := tokenPattern.FindString(reply); got != token {
		t.Fatalf("found %q in the answer", got)
	}
	if !refused("Sorry, too many attempts.") || refused("Alright, a new bot.") {
		t.Fatal("refusals are not told apart")
	}
}
