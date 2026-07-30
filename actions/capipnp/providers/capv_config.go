package providers

type VSphereCredentials struct {
	Username string `json:"username,omitempty" yaml:"username,omitempty"`
	Password string `json:"password,omitempty" yaml:"password,omitempty"`
}

type VSphereTemplate struct {
	Datacenter   string `json:"datacenter,omitempty" yaml:"datacenter,omitempty"`
	Datastore    string `json:"datastore,omitempty" yaml:"datastore,omitempty"`
	DiskGiB      int    `json:"diskGiB,omitempty" yaml:"diskGiB,omitempty"`
	Folder       string `json:"folder,omitempty" yaml:"folder,omitempty"`
	Host         string `json:"host,omitempty" yaml:"host,omitempty"`
	MemoryMiB    int    `json:"memoryMiB,omitempty" yaml:"memoryMiB,omitempty"`
	NetworkName  string `json:"networkName,omitempty" yaml:"networkName,omitempty"`
	NumCPUs      int    `json:"numCPUs,omitempty" yaml:"numCPUs,omitempty"`
	ResourcePool string `json:"resourcePool,omitempty" yaml:"resourcePool,omitempty"`
	Template     string `json:"template,omitempty" yaml:"template,omitempty"`
}
