// Package config handles application configuration.
package config

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// FlexBool is a boolean type that can be unmarshalled from a boolean, a string, or a number.
type FlexBool bool

// UnmarshalYAML implements the yaml.Unmarshaler interface for FlexBool.
func (fb *FlexBool) UnmarshalYAML(value *yaml.Node) error {
	switch value.Tag {
	case "!!bool":
		var b bool
		if err := value.Decode(&b); err != nil {
			return err
		}
		*fb = FlexBool(b)
	case "!!str":
		b, err := strconv.ParseBool(value.Value)
		if err != nil {
			return fmt.Errorf("cannot unmarshal string %q into FlexBool", value.Value)
		}
		*fb = FlexBool(b)
	case "!!int":
		i, err := strconv.Atoi(value.Value)
		if err != nil {
			return err
		}
		*fb = FlexBool(i != 0)
	case "!!float":
		f, err := strconv.ParseFloat(value.Value, 64)
		if err != nil {
			return err
		}
		*fb = FlexBool(f != 0)
	default:
		return fmt.Errorf("cannot unmarshal %s into FlexBool", value.Tag)
	}
	return nil
}
