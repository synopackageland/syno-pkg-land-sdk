package main

// Retrieval semantics are classified by exact API/method, intersected with the
// administrator-approved profile's version allowlist. Never infer read-only
// authority from a method prefix, profile name/purpose, or the response type:
// request/batch APIs can dispatch mutations even when named "get".
// Unclassified calls retain synchronous durable audit.
var readOnlyWebAPIMethods = map[string][]string{
	"SYNO.Cal.Cal":                 {"get", "list"},
	"SYNO.Cal.Event":               {"get", "list"},
	"SYNO.Cal.Todo":                {"get", "list"},
	"SYNO.MailClient.Mailbox":      {"get", "list"},
	"SYNO.MailClient.Thread":       {"list"},
	"SYNO.MailClient.Message":      {"get_original"},
	"SYNO.SynologyDrive.Files":     {"get", "list", "search", "download"},
	"SYNO.SynologyDrive.Labels":    {"get", "list"},
	"SYNO.Office.Node":             {"get", "list"},
	"SYNO.FileStation.Info":        {"get"},
	"SYNO.FileStation.List":        {"list", "list_share", "getinfo"},
	"SYNO.Core.System":             {"info"},
	"SYNO.Core.System.Utilization": {"get"},
	"SYNO.Core.Storage.Disk":       {"get", "list"},
	"SYNO.Core.Storage.Volume":     {"get", "list"},
	"SYNO.Core.Storage.Pool":       {"get", "list"},
	"SYNO.Core.Network":            {"get"},
	"SYNO.Core.Network.Interface":  {"get", "list"},
	"SYNO.Core.Network.Ethernet":   {"get", "list"},
}

func (p WebAPIProfile) readOnly(api, method string, version int) bool {
	return p.allows(api, method, version) && contains(readOnlyWebAPIMethods[api], method)
}
