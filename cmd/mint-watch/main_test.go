package main

import (
	"strings"
	"testing"
)

func TestDecodeBool(t *testing.T) {
	falseValue := make([]byte, 32)
	trueValue := make([]byte, 32)
	trueValue[31] = 1

	got, err := decodeBool(falseValue)
	if err != nil || got {
		t.Fatalf("decode false = %t, %v", got, err)
	}
	got, err = decodeBool(trueValue)
	if err != nil || !got {
		t.Fatalf("decode true = %t, %v", got, err)
	}
	if _, err = decodeBool([]byte{1}); err == nil {
		t.Fatal("expected short ABI result to fail")
	}
}

func TestParseHex(t *testing.T) {
	got, err := parseHex("0x1234")
	if err != nil || string(got) != string([]byte{0x12, 0x34}) {
		t.Fatalf("parseHex = %x, %v", got, err)
	}
	if _, err = parseHex("0x1"); err == nil || !strings.Contains(err.Error(), "even") {
		t.Fatalf("expected odd length error, got %v", err)
	}
}

func TestRequiredPositiveWei(t *testing.T) {
	got, err := requiredPositiveWei("42", "cap")
	if err != nil || got.Int64() != 42 {
		t.Fatalf("requiredPositiveWei = %v, %v", got, err)
	}
	if _, err := requiredPositiveWei("0", "cap"); err == nil {
		t.Fatal("expected zero cap to fail")
	}
}
