package main

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

// Normalize supported natural forms before Go's parser, preserving the exact
// option spelling in errors and never echoing an option's supplied value.
func (f *commandFlags) prepare(args []string) ([]string, error) {
	var options, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			key, _, equals := strings.Cut(arg, "=")
			name := strings.TrimLeft(key, "-")
			definition := f.Lookup(name)
			if definition == nil && name != "h" && name != "help" {
				var supported []string
				f.VisitAll(func(v *flag.Flag) { supported = append(supported, "--"+v.Name) })
				return nil, fmt.Errorf("未知选项 %s。%s 支持：%s；用 daylog %s -h 查看帮助", readable(key), f.Name(), strings.Join(supported, "、"), f.Name())
			}
			options = append(options, arg)
			boolean := false
			if definition != nil {
				if b, ok := definition.Value.(interface{ IsBoolFlag() bool }); ok {
					boolean = b.IsBoolFlag()
				}
			}
			if definition != nil && !equals && !boolean && i+1 < len(args) {
				i++
				options = append(options, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) == 0 {
		return options, nil
	}
	if len(positionals) == 1 {
		name, value := "", ""
		if f.Name() == "report" {
			for _, kind := range []string{"day", "week", "month"} {
				if positionals[0] == kind {
					name, value = "period", kind
				}
			}
		}
		if f.Name() == "today" && (positionals[0] == "today" || positionals[0] == "yesterday") {
			name = "date"
			date := time.Now()
			if positionals[0] == "yesterday" {
				date = date.AddDate(0, 0, -1)
			}
			value = date.Format("2006-01-02")
		}
		if name != "" {
			for _, option := range options {
				key, _, _ := strings.Cut(option, "=")
				if strings.TrimLeft(key, "-") == name {
					return nil, fmt.Errorf("自然写法不能与 --%s 同时使用", name)
				}
			}
			return append([]string{"--" + name, value}, options...), nil
		}
	}
	return nil, fmt.Errorf("不支持的位置参数；用 daylog %s -h 查看用法", f.Name())
}
