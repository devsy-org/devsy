package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// ErrUnsupportedType is returned if the type is not implemented.
var ErrUnsupportedType = errors.New("unsupported type")

// StrIntArray string array to be used on JSON UnmarshalJSON.
type StrIntArray []string

// UnmarshalJSON convert JSON object array of string or
// a string format strings to a golang string array.
func (sa *StrIntArray) UnmarshalJSON(data []byte) error {
	var jsonObj any
	err := json.Unmarshal(data, &jsonObj)
	if err != nil {
		return fmt.Errorf("unmarshal str int array: %w", err)
	}
	if obj, ok := jsonObj.([]any); ok {
		s, err := intArrayToStrings(obj)
		if err != nil {
			return err
		}
		*sa = StrIntArray(s)
		return nil
	}

	str, ok := scalarToString(jsonObj)
	if !ok {
		return ErrUnsupportedType
	}
	*sa = StrIntArray([]string{str})
	return nil
}

func scalarToString(v any) (string, bool) {
	switch value := v.(type) {
	case string:
		return value, true
	case int:
		return strconv.Itoa(value), true
	case float64:
		return strconv.Itoa(int(value)), true
	}
	return "", false
}

func intArrayToStrings(arr []any) ([]string, error) {
	s := make([]string, 0, len(arr))
	for _, v := range arr {
		str, ok := scalarToString(v)
		if !ok {
			return nil, ErrUnsupportedType
		}
		s = append(s, str)
	}
	return s, nil
}

func stringArray(arr []any) ([]string, error) {
	s := make([]string, 0, len(arr))
	for _, v := range arr {
		value, ok := v.(string)
		if !ok {
			return nil, ErrUnsupportedType
		}
		s = append(s, value)
	}
	return s, nil
}

// StrArray string array to be used on JSON UnmarshalJSON.
type StrArray []string

// UnmarshalJSON convert JSON object array of string or
// a string format strings to a golang string array.
func (sa *StrArray) UnmarshalJSON(data []byte) error {
	var jsonObj any
	err := json.Unmarshal(data, &jsonObj)
	if err != nil {
		return err
	}
	switch obj := jsonObj.(type) {
	case string:
		*sa = StrArray([]string{obj})
		return nil
	case []any:
		s := make([]string, 0, len(obj))
		for _, v := range obj {
			value, ok := v.(string)
			if !ok {
				return ErrUnsupportedType
			}
			s = append(s, value)
		}
		*sa = StrArray(s)
		return nil
	}
	return ErrUnsupportedType
}

type LifecycleHook map[string][]string

func (l *LifecycleHook) UnmarshalJSON(data []byte) error {
	*l = make(map[string][]string)

	var jsonObj any
	err := json.Unmarshal(data, &jsonObj)
	if err != nil {
		return err
	}
	switch obj := jsonObj.(type) {
	case string:
		// Anonymous string command
		(*l)[""] = []string{obj}
		return nil
	case []any:
		// Anonymous array of strings command
		cmd, err := stringArray(obj)
		if err != nil {
			return err
		}
		(*l)[""] = cmd
		return nil
	case map[string]any:
		return l.parseNamedCommands(obj)
	}

	return ErrUnsupportedType
}

func (l *LifecycleHook) parseNamedCommands(obj map[string]any) error {
	for k, v := range obj {
		value, ok := v.(string)
		if ok {
			// Named string command
			(*l)[k] = []string{value}
			continue
		}

		// Named array of strings command
		stringArrayValue, ok := v.([]any)
		if !ok {
			return ErrUnsupportedType
		}

		cmd, err := stringArray(stringArrayValue)
		if err != nil {
			return err
		}
		(*l)[k] = cmd
	}

	return nil
}

type StrBool string

// UnmarshalJSON parses fields that may be numbers or booleans.
func (s *StrBool) UnmarshalJSON(data []byte) error {
	var jsonObj any
	err := json.Unmarshal(data, &jsonObj)
	if err != nil {
		return err
	}
	switch obj := jsonObj.(type) {
	case string:
		*s = StrBool(obj)
		return nil
	case bool:
		*s = StrBool(strconv.FormatBool(obj))
		return nil
	}
	return ErrUnsupportedType
}

func (s *StrBool) Bool() (bool, error) {
	if s == nil || *s == "" {
		return false, nil
	}

	return strconv.ParseBool(string(*s))
}

type OptionEnum struct {
	Value       string `json:"value,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type OptionEnumArray []OptionEnum

func (e *OptionEnumArray) UnmarshalJSON(data []byte) error {
	var jsonObj any
	err := json.Unmarshal(data, &jsonObj)
	if err != nil {
		return err
	}
	obj, ok := jsonObj.([]any)
	if !ok {
		return ErrUnsupportedType
	}
	if len(obj) == 0 {
		*e = OptionEnumArray{}
		return nil
	}

	ret, err := parseOptionEnums(obj)
	if err != nil {
		return err
	}

	*e = OptionEnumArray(ret)
	return nil
}

func parseOptionEnums(obj []any) ([]OptionEnum, error) {
	ret := make([]OptionEnum, 0, len(obj))
	switch obj[0].(type) {
	case string:
		for _, v := range obj {
			if s, ok := v.(string); ok {
				ret = append(ret, OptionEnum{Value: s})
			}
		}
	case map[string]any:
		for _, v := range obj {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, ErrUnsupportedType
			}
			ret = append(ret, optionEnumFromMap(m))
		}
	default:
		return nil, ErrUnsupportedType
	}

	return ret, nil
}

func optionEnumFromMap(m map[string]any) OptionEnum {
	value := ""
	if s, ok := m["value"].(string); ok {
		value = s
	}
	displayName := ""
	if s, ok := m["displayName"].(string); ok {
		displayName = s
	}
	return OptionEnum{
		Value:       value,
		DisplayName: displayName,
	}
}
