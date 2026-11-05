package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]{0,3})$`)

var units = map[string]float64{
	"":  1,
	"B": 1,
	"K": 1 << 10, "KB": 1 << 10, "KIB": 1 << 10,
	"M": 1 << 20, "MB": 1 << 20, "MIB": 1 << 20,
	"G": 1 << 30, "GB": 1 << 30, "GIB": 1 << 30,
	"T": 1 << 40, "TB": 1 << 40, "TIB": 1 << 40,
}

func (b *ByteSize) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && value.Tag == "!!int" {
		n, err := strconv.ParseInt(value.Value, 10, 64)
		if err != nil {
			return err
		}
		*b = ByteSize(n)
		return nil
	}
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(value.Value))
	if m == nil {
		return fmt.Errorf("invalid byte size %q", value.Value)
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return err
	}
	u := strings.ToUpper(m[2])
	mult, ok := units[u]
	if !ok {
		return fmt.Errorf("unknown size unit %q", m[2])
	}
	*b = ByteSize(f * mult)
	return nil
}

func (b ByteSize) MarshalYAML() (any, error) {
	v := int64(b)
	for _, u := range []struct {
		name string
		n    int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}} {
		if v != 0 && v%u.n == 0 {
			return fmt.Sprintf("%d%s", v/u.n, u.name), nil
		}
	}
	return v, nil
}
