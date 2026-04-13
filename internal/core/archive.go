package core

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
	Name        string
	DisplayName string `yaml:"display-name"`
	Summary     string
	Description string
	Docs        string
	Issues      string
	Source      string
	Website     any
	Subordinate bool
	Bases       []CharmBase `yaml:"bases"`
	Resources   map[string]CharmResourceDeclaration
	Containers  map[string]CharmContainer
	Provides    map[string]Relation
	Requires    map[string]Relation
	Peers       map[string]Relation
	Assumes     any
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
	Resource string `yaml:"resource"`
}
