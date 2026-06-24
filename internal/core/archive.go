package core

import "gopkg.in/yaml.v3"

// CharmArchive contains the parsed files and manifest from an uploaded charm.
type CharmArchive struct {
	MetadataYAML string
	ManifestYAML string
	ConfigYAML   string
	ActionsYAML  string
	BundleYAML   string
	ReadmeMD     string
	Manifest     CharmManifest
}

// CharmBase represents a base entry declared in a charm manifest.
type CharmBase struct {
	Name          string   `yaml:"name"`
	Channel       string   `yaml:"channel"`
	Architecture  string   `yaml:"architecture"`
	Architectures []string `yaml:"architectures"`
}

// CharmManifest represents the parsed manifest for a charm archive.
type CharmManifest struct {
	Name          string
	DisplayName   string `yaml:"display-name"`
	Summary       string
	Description   string
	Maintainers   []string
	Docs          StringList
	Issues        StringList
	Source        StringList
	Website       any
	Terms         []string
	Subordinate   bool
	Bases         []CharmBase `yaml:"bases"`
	Resources     map[string]CharmResourceDeclaration
	Containers    map[string]CharmContainer
	Provides      map[string]Relation
	Requires      map[string]Relation
	Peers         map[string]Relation
	Storage       map[string]CharmStorage
	Devices       map[string]CharmDevice
	ExtraBindings map[string]any `yaml:"extra-bindings"`
	Assumes       any
	CharmUser     string `yaml:"charm-user"`
}

func (m *CharmManifest) UnmarshalYAML(value *yaml.Node) error {
	type manifest CharmManifest
	var decoded struct {
		manifest `yaml:",inline"`
		Peer     map[string]Relation `yaml:"peer"`
	}
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*m = CharmManifest(decoded.manifest)
	if len(decoded.Peer) > 0 {
		if m.Peers == nil {
			m.Peers = map[string]Relation{}
		}
		for name, relation := range decoded.Peer {
			m.Peers[name] = relation
		}
	}
	return nil
}

// StringList accepts charm metadata fields that Charmhub may publish as either
// a scalar string or a sequence of strings.
type StringList []string

func (s *StringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Value == "" {
			*s = nil
			return nil
		}
		*s = []string{value.Value}
		return nil
	case yaml.SequenceNode:
		out := make([]string, 0, len(value.Content))
		for _, item := range value.Content {
			var text string
			if err := item.Decode(&text); err != nil {
				return err
			}
			if text != "" {
				out = append(out, text)
			}
		}
		*s = out
		return nil
	case 0:
		*s = nil
		return nil
	default:
		var text string
		if err := value.Decode(&text); err != nil {
			return err
		}
		if text == "" {
			*s = nil
			return nil
		}
		*s = []string{text}
		return nil
	}
}

// CharmResourceDeclaration describes a resource declared by a charm manifest.
type CharmResourceDeclaration struct {
	Type           string `yaml:"type"`
	Description    string `yaml:"description"`
	Filename       string `yaml:"filename"`
	UpstreamSource string `yaml:"upstream-source"`
}

// CharmContainer describes a container entry declared by a charm manifest.
type CharmContainer struct {
	Resource string               `yaml:"resource"`
	Bases    []CharmContainerBase `yaml:"bases"`
	Mounts   []CharmContainerMount
	UID      int `yaml:"uid"`
	GID      int `yaml:"gid"`
}

type CharmContainerBase struct {
	Name          string   `yaml:"name"`
	Channel       string   `yaml:"channel"`
	Architectures []string `yaml:"architectures"`
}

type CharmContainerMount struct {
	Storage  string
	Location string
}

type CharmStorage struct {
	Type        string
	Description string
	Location    string
	ReadOnly    bool `yaml:"read-only"`
	Multiple    any
	MinimumSize string   `yaml:"minimum-size"`
	Properties  []string `yaml:"properties"`
}

type CharmDevice struct {
	Type        string
	Description string
	CountMin    int `yaml:"countmin"`
	CountMax    int `yaml:"countmax"`
}
