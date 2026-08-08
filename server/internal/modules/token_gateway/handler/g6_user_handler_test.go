package handler

import "testing"

func TestCSVSafeBlocksFormulaInjection(t *testing.T) {
	tests := map[string]string{
		"=HYPERLINK(\"https://invalid\")": "'=HYPERLINK(\"https://invalid\")",
		" +SUM(1,1)":                      "' +SUM(1,1)",
		"@cmd":                            "'@cmd",
		"req_123":                         "req_123",
	}
	for input, want := range tests {
		if got := csvSafe(input); got != want {
			t.Fatalf("CSV 单元格防护错误 input=%q got=%q want=%q", input, got, want)
		}
	}
}
