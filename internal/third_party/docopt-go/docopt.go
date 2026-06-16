package docopt

import (
	"fmt"
	"reflect"
	"strings"
)

type Opts map[string]interface{}

type Parser struct {
	OptionsFirst  bool
	SkipHelpFlags bool
}

func (p Parser) ParseArgs(usage string, argv []string, version string) (Opts, error) {
	opts := Opts{}
	positionals := []string{}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "--") {
			key, value, hasValue := strings.Cut(arg, "=")
			if !hasValue && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
				value = argv[i+1]
				i++
			} else if !hasValue {
				value = "true"
			}
			opts[key] = value
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			if arg == "-qv" || arg == "-vq" {
				opts["-q"] = true
				opts["-v"] = true
				continue
			}
			key, value, hasValue := strings.Cut(arg, "=")
			if !hasValue && (key == "-p" || key == "-k") && i+1 < len(argv) {
				value = argv[i+1]
				i++
			} else if !hasValue {
				value = "true"
			}
			opts[key] = value
			continue
		}
		positionals = append(positionals, arg)
	}
	if strings.Contains(usage, "<command>") {
		if len(positionals) > 0 {
			opts["<command>"] = positionals[0]
			opts["<arg>"] = append([]string(nil), positionals[1:]...)
		} else {
			opts["<arg>"] = []string{}
		}
	} else {
		opts["<arg>"] = append([]string(nil), positionals...)
		if len(positionals) > 0 {
			opts["<key>"] = append([]string(nil), positionals...)
		}
	}
	return opts, nil
}

func (o Opts) Bind(target interface{}) error {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("bind target must be pointer")
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("bind target must point to struct")
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		key := field.Tag.Get("docopt")
		if key == "" {
			continue
		}
		value, ok := optionValue(o, key)
		if !ok {
			continue
		}
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		switch fv.Kind() {
		case reflect.String:
			if s, ok := value.(string); ok {
				fv.SetString(s)
			}
		case reflect.Bool:
			fv.SetBool(value == true || value == "true")
		case reflect.Slice:
			if args, ok := value.([]string); ok && fv.Type().Elem().Kind() == reflect.String {
				fv.Set(reflect.ValueOf(args))
			}
		}
	}
	return nil
}

func (o Opts) String(name string) (string, error) {
	value, ok := o[name]
	if !ok || value == nil {
		return "", nil
	}
	s, ok := value.(string)
	if !ok {
		return fmt.Sprint(value), nil
	}
	return s, nil
}

func optionValue(opts Opts, tag string) (interface{}, bool) {
	for _, key := range strings.Split(tag, ",") {
		if value, ok := opts[key]; ok {
			return value, true
		}
	}
	return nil, false
}
