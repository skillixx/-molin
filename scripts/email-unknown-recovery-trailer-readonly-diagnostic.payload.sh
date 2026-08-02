#!/bin/bash
set -uo pipefail
umask 077
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

set +e
# Python 在内存中完成唯一文件发现、身份校验、严格解码和结构归类，shell 只接收固定协议字段。
parser_output=$(/usr/bin/python3 - 2>&1 <<'RECOVERY_TRAILER_DIAGNOSTIC'
import codecs
import os
import re
import stat

MAX_RECOVERY_BYTES = 64 * 1024 * 1024
READ_BUFFER_BYTES = 64 * 1024
RETAINED_LINE_BYTES = 512

class DiagnosticError(Exception):
    pass

def fail(classification):
    raise DiagnosticError(classification)

def bucket_count(value):
    if value == 0:
        return "0"
    if value == 1:
        return "1"
    return "2+"

def bucket_length(value):
    if value == 0:
        return "0"
    if value <= 64:
        return "1-64"
    if value <= 128:
        return "65-128"
    if value <= 256:
        return "129-256"
    return "257+"

def classify_suffix(line_bytes, line_length, line_ascii):
    if line_length > RETAINED_LINE_BYTES:
        return "other_ascii" if line_ascii else "nonascii"
    line = line_bytes.decode("utf-8", errors="strict")
    seconds = r"[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}"
    fractional = seconds + r"\.[0-9]+"
    if line == "-- Dump completed":
        return "undated"
    if re.fullmatch(r"-- Dump completed on " + seconds, line):
        return "dated_seconds"
    if re.fullmatch(r"-- Dump completed on " + fractional, line):
        return "dated_fractional"
    if re.fullmatch(r"-- Dump completed on (?:" + seconds + "|" + fractional + r") UTC", line):
        return "dated_utc"
    timezone = r"(?:Z|[+-][0-9]{2}:[0-9]{2}|[A-Za-z][A-Za-z0-9_+./-]{0,31})"
    if re.fullmatch(r"-- Dump completed on (?:" + seconds + "|" + fractional + r") " + timezone, line):
        return "dated_timezone"
    if all(ord(character) <= 0x7F for character in line):
        return "other_ascii"
    return "nonascii"

def classify_other_ascii_shape(line_bytes, line_length, suffix):
    if suffix != "other_ascii":
        return "not_applicable"
    if line_length > RETAINED_LINE_BYTES:
        return "other"
    line = line_bytes.decode("ascii", errors="strict")
    date = r"[0-9]{4}-[0-9]{2}-[0-9]{2}"
    time = r"[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?"
    if re.fullmatch(r"-- Dump completed[ -~]*\t[\t -~]*", line):
        return "tab"
    if re.fullmatch(r"-- Dump completed(?: on " + date + r" " + time + r"(?: [A-Za-z0-9_+./:()-]+)?)? +", line):
        return "trailing_space"
    if re.fullmatch(r"-- Dump completed on " + date + r" {2,}" + time, line):
        return "double_space"
    if re.fullmatch(r"-- Dump completed on " + date + r" " + time + r"[+-][0-9]{2}:[0-9]{2}", line):
        return "compact_offset_colon"
    if re.fullmatch(r"-- Dump completed on " + date + r" " + time + r" [+-][0-9]{4}", line):
        return "spaced_offset_nocolon"
    if re.fullmatch(r"-- Dump completed on " + date + r" " + time + r"[+-][0-9]{4}", line):
        return "compact_offset_nocolon"
    if re.fullmatch(r"-- Dump completed on " + date + r" " + time + r"Z", line):
        return "attached_z"
    if re.fullmatch(r"-- Dump completed on " + date + r"T" + time, line):
        return "t_separator"
    if re.fullmatch(r"-- Dump completed on " + date, line):
        return "date_only"
    if re.fullmatch(r"-- Dump completed on " + date + r" " + time + r" \([A-Za-z][A-Za-z0-9_+./-]{0,31}\)", line):
        return "timezone_parenthesized"
    return "other"

def bucket_runs(value, maximum):
    if value >= maximum:
        return str(maximum) + "+"
    return str(value)

def classify_lexical_buckets(line_bytes, line_length, suffix, alpha_runs, digit_runs, space_runs, hyphen_count, dot_count, separator_mask, digit_widths, space_widths):
    if suffix != "other_ascii":
        return ("not_applicable",) * 8
    completion_prefix = b"-- Dump completed"
    has_completion_prefix = line_bytes.startswith(completion_prefix)
    remainder = line_bytes[len(completion_prefix):] if has_completion_prefix else line_bytes
    # 流式计数包含整行；这里扣除固定前缀自身的两个字母段和两个空白段，只输出前缀后的不可逆桶。
    if has_completion_prefix:
        alpha_runs = max(0, alpha_runs - 2)
        if remainder[:1].isalpha():
            alpha_runs += 1
        space_runs = max(0, space_runs - 2)
        hyphen_count = max(0, hyphen_count - 2)
        space_widths = space_widths[2:]
    if remainder.startswith(b" on"):
        lead_token = "on"
    elif remainder.startswith(b" at"):
        lead_token = "at"
    elif remainder.startswith(b":"):
        lead_token = "colon"
    elif remainder.startswith(b"(") or remainder.startswith(b" ("):
        lead_token = "paren"
    elif remainder.startswith(b" ") or remainder.startswith(b"\t"):
        lead_token = "space"
    else:
        lead_token = "other"
    if line_length > RETAINED_LINE_BYTES:
        punctuation_mask = "mixed"
    else:
        text = remainder.decode("ascii", errors="strict")
        date = r"[0-9]{4}-[0-9]{2}-[0-9]{2}"
        time = r"[0-9]{2}:[0-9]{2}:[0-9]{2}"
        if re.fullmatch(r"[\t -~]*" + date + r"[ \tT]+" + time + r"(?:\.[0-9]+)?(?: ?(?:Z|[+-][0-9]{2}:?[0-9]{2}))[\t -~]*", text):
            punctuation_mask = "date_time_offset"
        elif re.fullmatch(r"[\t -~]*" + date + r"[ \tT]+" + time + r"\.[0-9]+[\t -~]*", text):
            punctuation_mask = "date_time_dot"
        elif re.fullmatch(r"[\t -~]*" + date + r"[ \tT]+" + time + r"[\t -~]*", text):
            punctuation_mask = "date_time"
        elif re.fullmatch(r"[A-Za-z0-9\t ]+", text):
            punctuation_mask = "letters_digits"
        else:
            punctuation_mask = "mixed"
    has_colon = (separator_mask & 1) != 0
    has_slash = (separator_mask & 2) != 0
    has_dot = dot_count > 0
    if (separator_mask & 8) != 0:
        separator_profile = "contains_semicolon"
    elif (separator_mask & 16) != 0:
        separator_profile = "contains_comma"
    elif (separator_mask & 32) != 0:
        separator_profile = "contains_paren"
    elif hyphen_count > 0 and has_colon:
        separator_profile = "hyphen_colon"
    elif has_slash and has_colon:
        separator_profile = "slash_colon"
    elif has_dot and has_colon:
        separator_profile = "dot_colon"
    elif hyphen_count > 0 and has_dot:
        separator_profile = "hyphen_dot"
    elif has_slash and has_dot:
        separator_profile = "slash_dot"
    elif dot_count >= 2:
        separator_profile = "dot_dot"
    else:
        separator_profile = "contains_other"
    if digit_runs != 6 or len(digit_widths) != 6:
        field_width_profile = "other"
    else:
        expected_widths = (4, 2, 2, 2, 2, 2)
        has_short = any(actual < expected for actual, expected in zip(digit_widths, expected_widths))
        has_long = any(actual > expected for actual, expected in zip(digit_widths, expected_widths))
        if has_short and has_long:
            field_width_profile = "mixed"
        elif has_short:
            field_width_profile = "has_short"
        elif has_long:
            field_width_profile = "has_long"
        else:
            field_width_profile = "expected"
    if space_runs != 3 or len(space_widths) != 3:
        space_width_profile = "other"
    else:
        multiple = [width > 1 for width in space_widths]
        if not any(multiple):
            space_width_profile = "all_single"
        elif sum(multiple) > 1:
            space_width_profile = "multiple_multi"
        elif multiple[0]:
            space_width_profile = "after_prefix_multi"
        elif multiple[1]:
            space_width_profile = "after_on_multi"
        else:
            space_width_profile = "between_multi"
    return (
        lead_token,
        bucket_runs(alpha_runs, 3),
        bucket_runs(digit_runs, 7),
        bucket_runs(space_runs, 4),
        punctuation_mask,
        separator_profile,
        field_width_profile,
        space_width_profile,
    )

def analyze_stream(stream):
    decoder = codecs.getincrementaldecoder("utf-8")(errors="strict")
    completion_prefix = b"-- Dump completed"
    completion_count = 0
    trailing_blank_lines = 0
    current_sample = bytearray()
    current_length = 0
    current_ascii = True
    current_alpha_runs = 0
    current_digit_runs = 0
    current_space_runs = 0
    current_run_kind = None
    current_hyphen_count = 0
    current_dot_count = 0
    current_separator_mask = 0
    current_digit_widths = []
    current_space_widths = []
    last_sample = b""
    last_length = 0
    last_ascii = True
    last_is_completion_prefix = False
    last_alpha_runs = 0
    last_digit_runs = 0
    last_space_runs = 0
    last_hyphen_count = 0
    last_dot_count = 0
    last_separator_mask = 0
    last_digit_widths = ()
    last_space_widths = ()
    total_bytes = 0
    eof_tail = b""
    max_retained_state = 0

    def append_segment(segment):
        nonlocal current_length, current_ascii, current_alpha_runs, current_digit_runs, current_space_runs, current_run_kind
        nonlocal current_hyphen_count, current_dot_count, current_separator_mask
        nonlocal current_digit_widths, current_space_widths, max_retained_state
        current_length += len(segment)
        current_ascii = current_ascii and segment.isascii()
        current_hyphen_count = min(3, current_hyphen_count + segment.count(b"-"))
        current_dot_count = min(2, current_dot_count + segment.count(b"."))
        if b":" in segment:
            current_separator_mask |= 1
        if b"/" in segment:
            current_separator_mask |= 2
        if b";" in segment:
            current_separator_mask |= 8
        if b"," in segment:
            current_separator_mask |= 16
        if b"(" in segment or b")" in segment:
            current_separator_mask |= 32
        position = 0
        for match in re.finditer(rb"[A-Za-z]+|[0-9]+|[ \t]+", segment):
            if match.start() > position:
                current_run_kind = None
            token = match.group(0)
            kind = "alpha" if token[:1].isalpha() else ("digit" if token[:1].isdigit() else "space")
            if kind == "digit":
                if kind != current_run_kind:
                    if len(current_digit_widths) < 6:
                        current_digit_widths.append(min(5, len(token)))
                elif current_digit_runs <= 6 and current_digit_widths:
                    current_digit_widths[-1] = min(5, current_digit_widths[-1] + len(token))
            elif kind == "space":
                if kind != current_run_kind:
                    if len(current_space_widths) < 5:
                        current_space_widths.append(min(2, len(token)))
                elif current_space_runs <= 5 and current_space_widths:
                    current_space_widths[-1] = min(2, current_space_widths[-1] + len(token))
            if kind != current_run_kind:
                if kind == "alpha":
                    # 固定前缀含两个字母段，先保留到五段，扣除前缀后仍可准确判定 3+。
                    current_alpha_runs = min(5, current_alpha_runs + 1)
                elif kind == "digit":
                    current_digit_runs = min(7, current_digit_runs + 1)
                else:
                    # 固定前缀含两个空白段，先保留到六段，扣除前缀后仍可准确判定 4+。
                    current_space_runs = min(6, current_space_runs + 1)
            current_run_kind = kind
            position = match.end()
        if position < len(segment):
            current_run_kind = None
        remaining = RETAINED_LINE_BYTES - len(current_sample)
        if remaining > 0:
            current_sample.extend(segment[:remaining])
        max_retained_state = max(max_retained_state, len(current_sample) + len(last_sample))

    def finish_line():
        nonlocal completion_count, trailing_blank_lines, current_length, current_ascii
        nonlocal current_alpha_runs, current_digit_runs, current_space_runs, current_run_kind
        nonlocal current_hyphen_count, current_dot_count, current_separator_mask
        nonlocal current_digit_widths, current_space_widths
        nonlocal last_sample, last_length, last_ascii, last_is_completion_prefix, last_alpha_runs, last_digit_runs, last_space_runs
        nonlocal last_hyphen_count, last_dot_count, last_separator_mask, max_retained_state
        nonlocal last_digit_widths, last_space_widths
        logical_length = current_length
        logical_sample = bytes(current_sample)
        if logical_length > 0 and logical_sample.endswith(b"\r"):
            logical_length -= 1
            logical_sample = logical_sample[:-1]
        is_prefix = logical_sample.startswith(completion_prefix)
        if is_prefix:
            completion_count = min(2, completion_count + 1)
        if logical_length == 0:
            trailing_blank_lines = min(2, trailing_blank_lines + 1)
        else:
            trailing_blank_lines = 0
            last_sample = logical_sample
            last_length = logical_length
            last_ascii = current_ascii
            last_is_completion_prefix = is_prefix
            last_alpha_runs = current_alpha_runs
            last_digit_runs = current_digit_runs
            last_space_runs = current_space_runs
            last_hyphen_count = current_hyphen_count
            last_dot_count = current_dot_count
            last_separator_mask = current_separator_mask
            last_digit_widths = tuple(current_digit_widths)
            last_space_widths = tuple(current_space_widths)
        current_sample.clear()
        current_length = 0
        current_ascii = True
        current_alpha_runs = 0
        current_digit_runs = 0
        current_space_runs = 0
        current_run_kind = None
        current_hyphen_count = 0
        current_dot_count = 0
        current_separator_mask = 0
        current_digit_widths = []
        current_space_widths = []
        max_retained_state = max(max_retained_state, len(last_sample))

    try:
        while True:
            chunk = stream.read(READ_BUFFER_BYTES)
            if not chunk:
                break
            total_bytes += len(chunk)
            if total_bytes > MAX_RECOVERY_BYTES:
                fail("recovery_size")
            decoder.decode(chunk, final=False)
            eof_tail = (eof_tail + chunk)[-2:]
            parts = chunk.split(b"\n")
            for index, segment in enumerate(parts):
                append_segment(segment)
                if index < len(parts) - 1:
                    finish_line()
        decoder.decode(b"", final=True)
    except UnicodeDecodeError:
        fail("recovery_utf8")
    if current_length > 0:
        finish_line()

    if eof_tail.endswith(b"\r\n"):
        eof_type = "CRLF"
    elif eof_tail.endswith(b"\n"):
        eof_type = "LF"
    elif eof_tail.endswith(b"\r"):
        eof_type = "other"
    else:
        eof_type = "no_newline"
    suffix = classify_suffix(last_sample, last_length, last_ascii)
    lead_token, alpha_runs, digit_runs, space_runs, punctuation_mask, separator_profile, field_width_profile, space_width_profile = classify_lexical_buckets(
        last_sample, last_length, suffix, last_alpha_runs, last_digit_runs, last_space_runs,
        last_hyphen_count, last_dot_count, last_separator_mask, last_digit_widths, last_space_widths
    )
    result = (
        "completion_prefix_count=" + bucket_count(completion_count) +
        " last_nonempty_is_completion_prefix=" + str(last_is_completion_prefix).lower() +
        " eof_type=" + eof_type +
        " trailing_blank_lines=" + bucket_count(trailing_blank_lines) +
        " suffix=" + suffix +
        " other_ascii_shape=" + classify_other_ascii_shape(last_sample, last_length, suffix) +
        " lead_token=" + lead_token +
        " alpha_runs=" + alpha_runs +
        " digit_runs=" + digit_runs +
        " space_runs=" + space_runs +
        " punctuation_mask=" + punctuation_mask +
        " separator_profile=" + separator_profile +
        " field_width_profile=" + field_width_profile +
        " space_width_profile=" + space_width_profile +
        " last_line_length=" + bucket_length(last_length)
    )
    return result, max_retained_state

def discover_and_analyze():
    rollback_dir = "/home/pc/molin/rollback"
    try:
        with os.scandir(rollback_dir) as iterator:
            candidates = [entry.path for entry in iterator if re.fullmatch(r"molin-email-unknown-[a-f0-9]{32}\.sql", entry.name) and entry.is_file(follow_symlinks=False)]
    except OSError:
        fail("recovery_find")
    if len(candidates) != 1:
        fail("recovery_find")
    path = candidates[0]
    try:
        current_uid = os.getuid()
    except (AttributeError, OSError):
        fail("recovery_uid")
    try:
        before = os.lstat(path)
    except OSError:
        fail("recovery_stat")
    if not stat.S_ISREG(before.st_mode) or before.st_uid != current_uid or stat.S_IMODE(before.st_mode) != 0o600 or before.st_size <= 0:
        fail("recovery_stat")
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags)
    except OSError:
        fail("recovery_read")
    try:
        opened = os.fstat(descriptor)
        identity_before = (before.st_dev, before.st_ino, before.st_uid, before.st_mode, before.st_nlink, before.st_size, before.st_mtime_ns, before.st_ctime_ns)
        identity_opened = (opened.st_dev, opened.st_ino, opened.st_uid, opened.st_mode, opened.st_nlink, opened.st_size, opened.st_mtime_ns, opened.st_ctime_ns)
        if identity_opened != identity_before or not stat.S_ISREG(opened.st_mode):
            fail("recovery_stat")
        with os.fdopen(os.dup(descriptor), "rb", buffering=0) as stream:
            result, retained_state = analyze_stream(stream)
        if retained_state > RETAINED_LINE_BYTES * 2:
            fail("unclassified")
        after = os.fstat(descriptor)
        identity_after = (after.st_dev, after.st_ino, after.st_uid, after.st_mode, after.st_nlink, after.st_size, after.st_mtime_ns, after.st_ctime_ns)
        if identity_after != identity_opened:
            fail("recovery_stat")
    except DiagnosticError:
        raise
    except OSError:
        fail("recovery_read")
    finally:
        os.close(descriptor)
    return result

try:
    print("SAFE_TRAILER_RESULT=" + discover_and_analyze())
except DiagnosticError as error:
    classification = str(error)
    if classification not in {"recovery_find", "recovery_uid", "recovery_stat", "recovery_read", "recovery_size", "recovery_utf8"}:
        classification = "unclassified"
    print("SAFE_TRAILER_ERROR=" + classification)
except Exception:
    print("SAFE_TRAILER_ERROR=unclassified")
RECOVERY_TRAILER_DIAGNOSTIC
)
parser_exit=$?
set -e

if [[ $parser_exit -eq 0 && "$parser_output" =~ ^SAFE_TRAILER_RESULT=(completion_prefix_count=(0|1|2\+)\ last_nonempty_is_completion_prefix=(true|false)\ eof_type=(no_newline|LF|CRLF|other)\ trailing_blank_lines=(0|1|2\+)\ (suffix=other_ascii\ other_ascii_shape=(trailing_space|double_space|tab|compact_offset_colon|spaced_offset_nocolon|compact_offset_nocolon|attached_z|t_separator|date_only|timezone_parenthesized|other)\ lead_token=(on|at|colon|paren|space|other)\ alpha_runs=(0|1|2|3\+)\ digit_runs=(0|1|2|3|4|5|6|7\+)\ space_runs=(0|1|2|3|4\+)\ punctuation_mask=(date_time|date_time_dot|date_time_offset|letters_digits|mixed)\ separator_profile=(hyphen_colon|slash_colon|dot_colon|hyphen_dot|slash_dot|dot_dot|contains_semicolon|contains_comma|contains_paren|contains_other)\ field_width_profile=(expected|has_short|has_long|mixed|other)\ space_width_profile=(all_single|after_prefix_multi|after_on_multi|between_multi|multiple_multi|other)|suffix=(undated|dated_seconds|dated_fractional|dated_utc|dated_timezone|nonascii)\ other_ascii_shape=not_applicable\ lead_token=not_applicable\ alpha_runs=not_applicable\ digit_runs=not_applicable\ space_runs=not_applicable\ punctuation_mask=not_applicable\ separator_profile=not_applicable\ field_width_profile=not_applicable\ space_width_profile=not_applicable)\ last_line_length=(0|1-64|65-128|129-256|257\+))$ ]]; then
  /usr/bin/printf 'status=pass classification=pass candidate_unique=true file_identity=true %s writes=false database=false redis=false retries=0\n' "${BASH_REMATCH[1]}"
  exit 0
fi

if [[ $parser_exit -eq 0 && "$parser_output" =~ ^SAFE_TRAILER_ERROR=(recovery_find|recovery_uid|recovery_stat|recovery_read|recovery_size|recovery_utf8|unclassified)$ ]]; then
  classification=${BASH_REMATCH[1]}
  candidate_unique=true
  file_identity=true
  if [[ "$classification" == recovery_find ]]; then
    candidate_unique=false
    file_identity=false
  elif [[ "$classification" == recovery_uid || "$classification" == recovery_stat || "$classification" == recovery_read ]]; then
    file_identity=false
  fi
  /usr/bin/printf 'status=pass classification=%s candidate_unique=%s file_identity=%s writes=false database=false redis=false retries=0\n' "$classification" "$candidate_unique" "$file_identity"
  exit 0
fi

/usr/bin/printf 'status=failed classification=diagnostic_protocol candidate_unique=false file_identity=false writes=false database=false redis=false retries=0\n'
exit 2
