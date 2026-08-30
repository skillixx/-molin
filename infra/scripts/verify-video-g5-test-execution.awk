# 显式要求的顶层用例必须真正运行且通过，禁止零匹配、跳过或重复执行被包装成兼容PASS。
BEGIN {
  count = split(required, names, ",")
  for (i = 1; i <= count; i++) {
    if (names[i] != "") expected[names[i]] = 1
  }
}
{
  print
  if ($1 == "===" && $2 == "RUN" && ($3 in expected)) runs[$3]++
  if ($1 == "---" && $2 == "PASS:" && ($3 in expected)) passes[$3]++
  if ($1 == "---" && ($2 == "SKIP:" || $2 == "FAIL:") && ($3 in expected)) rejected[$3] = 1
}
END {
  invalid = 0
  for (name in expected) {
    if (runs[name] != 1 || passes[name] != 1 || rejected[name]) {
      print "VIDEO_G5_REQUIRED_TESTS=FAILED test=" name " run=" (runs[name]+0) " pass=" (passes[name]+0)
      invalid = 1
    }
  }
  if (invalid) exit 1
  if (required != "") print "VIDEO_G5_REQUIRED_TESTS=PASS tests=" required
}
