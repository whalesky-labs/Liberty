package credentials

import (
	"strings"
	"testing"
)

func TestSecurityPasswordOutputDistinguishesHexAndAscii(t *testing.T) {
	for _, value := range []struct{ input, want string }{{`password: "deadbeef"`, "deadbeef"}, {`password: 0xe6b58be8af95  "display"`, "测试"}, {`password: ""`, ""}} {
		got, err := parseSecurityPassword([]byte(value.input))
		if err != nil || got.Reveal() != value.want {
			t.Fatal("credential parse differs")
		}
	}
	for _, input := range []string{"unexpected", "password: 0xzz", `password: "unclosed`} {
		if _, err := parseSecurityPassword([]byte(input)); err == nil {
			t.Fatal("malformed credential accepted")
		}
	}
	if securityQuote("a\"b\\c $literal") != `"a\"b\\c $literal"` {
		t.Fatal("command quoting differs")
	}
}
func TestCredentialOutputBounded(t *testing.T) {
	var output boundedOutput
	if count, err := output.Write([]byte(strings.Repeat("x", 40000))); err != nil || count != 40000 || len(output.bytes) != 32768 || !output.overflow {
		t.Fatal("output limit differs")
	}
	if count, err := output.Write([]byte("more")); err != nil || count != 4 || len(output.bytes) != 32768 {
		t.Fatal("output overflow changed bound")
	}
}
