package strutil

import "testing"

func TestReverse(t *testing.T) {
	if Reverse("héllo") != "olléh" {
		t.Fatal(Reverse("héllo"))
	}
}

func TestIsPalindrome(t *testing.T) {
	if !IsPalindrome("A man, a plan, a canal: Panama") || IsPalindrome("abc") {
		t.Fatal("palindrome")
	}
}
