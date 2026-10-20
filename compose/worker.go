package compose

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// The worker reads an already interpolated model a second time. Escape dollar
// values everywhere except the SDK-owned identity and mount placeholders. This
// prevents host process variables from changing literal app data or shell argv.
func workerYAML(raw []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var walk func(*yaml.Node, []string)
	walk = func(n *yaml.Node, path []string) {
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, path)
			}
		case yaml.MappingNode:
			for i := 0; i < len(n.Content); i += 2 {
				walk(n.Content[i+1], append(append([]string{}, path...), n.Content[i].Value))
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, append(append([]string{}, path...), strconv.Itoa(i)))
			}
		case yaml.ScalarNode:
			if n.Tag != "!!str" {
				return
			}
			ownedUser := len(path) == 3 && path[0] == "services" && path[2] == "user" && n.Value == PackageUser
			ownedMount := len(path) == 5 && path[0] == "services" && path[2] == "volumes" && path[4] == "source" && (strings.HasPrefix(n.Value, "${SYNOPKG_PKGVAR}/") || strings.HasPrefix(n.Value, "${SYNOPKGLAND_MOUNT_"))
			if !ownedUser && !ownedMount {
				n.Value = strings.ReplaceAll(n.Value, "$", "$$")
			}
		}
	}
	walk(&doc, nil)
	return yaml.Marshal(&doc)
}
