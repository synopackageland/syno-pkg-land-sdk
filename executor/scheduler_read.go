package main

import (
	"encoding/json"
	"strings"
)

// DSM's EventScheduler initialization writes its SQLite database even on a get
// or a combined TaskScheduler list. Only these fixed reads need isolation.
func schedulerReadCommand(args []string) bool {
	api, method := "", ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "api=") {
			api = strings.TrimPrefix(arg, "api=")
		}
		if strings.HasPrefix(arg, "method=") {
			method = strings.TrimPrefix(arg, "method=")
		}
	}
	return (api == "SYNO.Core.EventScheduler" && method == "get") || (api == "SYNO.Core.TaskScheduler" && method == "list")
}

func validSchedulerRead(args []string) bool {
	if len(args) < 4 || args[0] != "--exec" {
		return false
	}
	fields := map[string]string{}
	for _, arg := range args[1:] {
		key, value, ok := strings.Cut(arg, "=")
		_, duplicate := fields[key]
		if !ok || duplicate {
			return false
		}
		fields[key] = value
	}
	if fields["api"] == "SYNO.Core.EventScheduler" && fields["method"] == "get" && fields["version"] == "1" && len(fields) == 4 {
		var name string
		if json.Unmarshal([]byte(fields["task_name"]), &name) != nil {
			return false
		}
		pkg, ok := strings.CutSuffix(name, " executor boot")
		return ok && tokenRE.MatchString(pkg)
	}
	if fields["api"] != "SYNO.Core.TaskScheduler" || fields["method"] != "list" || len(fields) != 5 || fields["limit"] != "500" {
		return false
	}
	return (fields["version"] == "3" && fields["start"] == "0") || (fields["version"] == "2" && fields["offset"] == "0")
}
