package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// decodeStrictJSONObject 先遍历 JSON token 树拒绝任意层级的重复对象键，再按目标结构拒绝未知字段。
// 不能直接反序列化到 map 或 struct，否则相同键的后值会静默覆盖前值，破坏管理端版本与审计语义。
func decodeStrictJSONObject(raw []byte, target interface{}) error {
	validator := json.NewDecoder(bytes.NewReader(raw))
	validator.UseNumber()
	first, err := validator.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("请求体必须是 JSON 对象")
	}
	if err := consumeJSONObject(validator); err != nil {
		return err
	}
	if _, err := validator.Token(); !errors.Is(err, io.EOF) {
		return errors.New("请求体包含尾随内容")
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("请求体包含尾随内容")
	}
	return nil
}

func consumeJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("JSON 对象键格式错误")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("JSON 对象包含重复键")
		}
		seen[key] = struct{}{}
		if err := consumeJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("JSON 对象未正确结束")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		return consumeJSONObject(decoder)
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("JSON 数组未正确结束")
		}
		return nil
	default:
		return errors.New("JSON 值结构错误")
	}
}
