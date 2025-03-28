package observability

type StackStateConfig struct {
	ServiceToken   string `json:"serviceToken" yaml:"serviceToken"`
	Url            string `json:"url" yaml:"url"`
	ClusterApiKey  string `json:"clusterApiKey" yaml:"clusterApiKey"`
	UpgradeVersion string `json:"upgradeVersion" yaml:"upgradeVersion"`
	License        string `json:"license" yaml:"license"`
	AdminPassword  string `json:"adminPassword" yaml:"adminPassword"`
}

// GlobalConfig represents global configuration values
type GlobalConfig struct {
	ImageRegistry string `json:"imageRegistry" yaml:"imageRegistry"`
}

// AuthenticationConfig represents the authentication configuration
type AuthenticationConfig struct {
	AdminPassword string `json:"adminPassword" yaml:"adminPassword"`
}

// ApiKeyConfig represents API key configuration
type ApiKeyConfig struct {
	Key string `json:"key" yaml:"key"`
}

// LicenseConfig represents the license configuration
type LicenseConfig struct {
	Key string `json:"key" yaml:"key"`
}

// StackstateServerConfig groups the various StackState configuration options
type StackstateServerConfig struct {
	BaseUrl        string               `json:"baseUrl" yaml:"baseUrl"`
	Authentication AuthenticationConfig `json:"authentication" yaml:"authentication"`
	ApiKey         ApiKeyConfig         `json:"apiKey" yaml:"apiKey"`
	License        LicenseConfig        `json:"license" yaml:"license"`
}

// BaseConfig represents the base configuration values
type BaseConfig struct {
	Global     GlobalConfig           `json:"global" yaml:"global"`
	Stackstate StackstateServerConfig `json:"stackstate" yaml:"stackstate"`
}

// ResourcesConfig defines common CPU and Memory configurations for Requests and Limits
type ResourcesConfig struct {
	CPU    string `json:"cpu" yaml:"cpu"`
	Memory string `json:"memory" yaml:"memory"`
}

// PersistenceConfig defines common persistence configurations like size
type PersistenceConfig struct {
	Size string `json:"size" yaml:"size"`
}

// SizingConfigNonHA represents the sizing configuration values
type SizingConfigNonHA struct {
	Clickhouse struct {
		ReplicaCount int               `json:"replicaCount" yaml:"replicaCount"`
		Persistence  PersistenceConfig `json:"persistence" yaml:"persistence"`
	} `json:"clickhouse" yaml:"clickhouse"`

	Elasticsearch struct {
		ExporterResources  ResourcesConfig `json:"prometheusElasticsearchExporterResources" yaml:"prometheusElasticsearchExporterResources"`
		MinimumMasterNodes int             `json:"minimumMasterNodes" yaml:"minimumMasterNodes"`
		Replicas           int             `json:"replicas" yaml:"replicas"`
		EsJavaOpts         string          `json:"esJavaOpts" yaml:"esJavaOpts"`
		Resources          struct {
			Requests ResourcesConfig `json:"requests" yaml:"requests"`
			Limits   ResourcesConfig `json:"limits" yaml:"limits"`
		} `json:"resources" yaml:"resources"`
		VolumeClaimTemplate struct {
			Requests struct {
				Storage string `json:"storage" yaml:"storage"`
			} `json:"requests" yaml:"requests"`
		} `json:"volumeClaimTemplate" yaml:"volumeClaimTemplate"`
	} `json:"elasticsearch" yaml:"elasticsearch"`

	Hbase struct {
		Version    string `json:"version" yaml:"version"`
		Deployment struct {
			Mode string `json:"mode" yaml:"mode"`
		} `json:"deployment" yaml:"deployment"`
		Stackgraph struct {
			Persistence PersistenceConfig `json:"persistence" yaml:"persistence"`
			Resources   struct {
				Requests ResourcesConfig `json:"requests" yaml:"requests"`
				Limits   ResourcesConfig `json:"limits" yaml:"limits"`
			} `json:"resources" yaml:"resources"`
		} `json:"stackgraph" yaml:"stackgraph"`
		Tephra struct {
			Resources struct {
				Requests ResourcesConfig `json:"requests" yaml:"requests"`
				Limits   ResourcesConfig `json:"limits" yaml:"limits"`
			} `json:"resources" yaml:"resources"`
			ReplicaCount int `json:"replicaCount" yaml:"replicaCount"`
		} `json:"tephra" yaml:"tephra"`
	} `json:"hbase" yaml:"hbase"`

	Kafka struct {
		DefaultReplicationFactor             int `json:"defaultReplicationFactor" yaml:"defaultReplicationFactor"`
		OffsetsTopicReplicationFactor        int `json:"offsetsTopicReplicationFactor" yaml:"offsetsTopicReplicationFactor"`
		ReplicaCount                         int `json:"replicaCount" yaml:"replicaCount"`
		TransactionStateLogReplicationFactor int `json:"transactionStateLogReplicationFactor" yaml:"transactionStateLogReplicationFactor"`
		Resources                            struct {
			Requests ResourcesConfig `json:"requests" yaml:"requests"`
			Limits   ResourcesConfig `json:"limits" yaml:"limits"`
		} `json:"resources" yaml:"resources"`
		Persistence PersistenceConfig `json:"persistence" yaml:"persistence"`
	} `json:"kafka" yaml:"kafka"`

	Stackstate struct {
		Experimental struct {
			Server struct {
				Split bool `json:"split" yaml:"split"`
			} `json:"server" yaml:"server"`
		} `json:"experimental" yaml:"experimental"`
		Components struct {
			All struct {
				ExtraEnv struct {
					Open map[string]string `json:"open" yaml:"open"` // Simplified with a map
				} `json:"extraEnv" yaml:"extraEnv"`
			} `json:"all" yaml:"all"`
			Server struct {
				ExtraEnv struct {
					Open map[string]string `json:"open" yaml:"open"` // Simplified with a map
				} `json:"extraEnv" yaml:"extraEnv"`
				Resources struct {
					Limits   ResourcesConfig `json:"limits" yaml:"limits"`
					Requests ResourcesConfig `json:"requests" yaml:"requests"`
				} `json:"resources" yaml:"resources"`
			} `json:"server" yaml:"server"`
		} `json:"components" yaml:"components"`
	} `json:"stackstate" yaml:"stackstate"`
}

type SizingConfigHA struct {
	Clickhouse struct {
		ReplicaCount int `yaml:"replicaCount"`
		Persistence  struct {
			Size string `yaml:"size"`
		} `yaml:"persistence"`
	} `yaml:"clickhouse"`
	Elasticsearch struct {
		VolumeClaimTemplate struct {
			Resources struct {
				Requests struct {
					Storage string `yaml:"storage"`
				} `yaml:"requests"`
			} `yaml:"resources"`
		} `yaml:"volumeClaimTemplate"`
	} `yaml:"elasticsearch"`
	Hbase struct {
		Version    string `yaml:"version"`
		Deployment struct {
			Mode string `yaml:"mode"`
		} `yaml:"deployment"`
		Hbase struct {
			Console struct {
				Resources struct {
					Requests struct {
						CPU string `yaml:"cpu"`
					} `yaml:"requests"`
				} `yaml:"resources"`
			} `yaml:"console"`
			Master struct {
				Resources struct {
					Requests struct {
						CPU string `yaml:"cpu"`
					} `yaml:"requests"`
					Limits struct {
						EphemeralStorage string `yaml:"ephemeral-storage"`
						CPU              string `yaml:"cpu"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"master"`
			Regionserver struct {
				Resources struct {
					Requests struct {
						CPU string `yaml:"cpu"`
					} `yaml:"requests"`
					Limits struct {
						EphemeralStorage string `yaml:"ephemeral-storage"`
						CPU              string `yaml:"cpu"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"regionserver"`
		} `yaml:"hbase"`
		Hdfs struct {
			Datanode struct {
				Resources struct {
					Requests struct {
						CPU string `yaml:"cpu"`
					} `yaml:"requests"`
					Limits struct {
						EphemeralStorage string `yaml:"ephemeral-storage"`
						CPU              string `yaml:"cpu"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"datanode"`
			Namenode struct {
				Resources struct {
					Requests struct {
						CPU string `yaml:"cpu"`
					} `yaml:"requests"`
					Limits struct {
						EphemeralStorage string `yaml:"ephemeral-storage"`
						CPU              string `yaml:"cpu"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"namenode"`
			Secondarynamenode struct {
				Enabled   bool `yaml:"enabled"`
				Resources struct {
					Requests struct {
						CPU string `yaml:"cpu"`
					} `yaml:"requests"`
					Limits struct {
						EphemeralStorage string `yaml:"ephemeral-storage"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"secondarynamenode"`
		} `yaml:"hdfs"`
		Tephra struct {
			Resources struct {
				Limits struct {
					Memory string `yaml:"memory"`
					CPU    string `yaml:"cpu"`
				} `yaml:"limits"`
				Requests struct {
					Memory string `yaml:"memory"`
					CPU    string `yaml:"cpu"`
				} `yaml:"requests"`
			} `yaml:"resources"`
			ReplicaCount int `yaml:"replicaCount"`
		} `yaml:"tephra"`
	} `yaml:"hbase"`
	Kafka struct {
		TransactionStateLogReplicationFactor int `yaml:"transactionStateLogReplicationFactor"`
		Resources                            struct {
			Requests struct {
				CPU    string `yaml:"cpu"`
				Memory string `yaml:"memory"`
			} `yaml:"requests"`
			Limits struct {
				CPU              string `yaml:"cpu"`
				Memory           string `yaml:"memory"`
				EphemeralStorage string `yaml:"ephemeral-storage"`
			} `yaml:"limits"`
		} `yaml:"resources"`
	} `yaml:"kafka"`
	Stackstate struct {
		Experimental struct {
			Server struct {
				Split bool `yaml:"split"`
			} `yaml:"server"`
		} `yaml:"experimental"`
		Components struct {
			All struct {
				ExtraEnv struct {
					Open struct {
						CONFIGFORCEStackstateAgentsAgentLimit string `yaml:"CONFIG_FORCE_stackstate_agents_agentLimit"`
					} `yaml:"open"`
				} `yaml:"extraEnv"`
			} `yaml:"all"`
			API struct {
				Resources struct {
					Requests struct {
						CPU    string `yaml:"cpu"`
						Memory string `yaml:"memory"`
					} `yaml:"requests"`
					Limits struct {
						EphemeralStorage string `yaml:"ephemeral-storage"`
						CPU              string `yaml:"cpu"`
						Memory           string `yaml:"memory"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"api"`
			Checks struct {
				Resources struct {
					Requests struct {
						CPU    string `yaml:"cpu"`
						Memory string `yaml:"memory"`
					} `yaml:"requests"`
					Limits struct {
						CPU              string `yaml:"cpu"`
						Memory           string `yaml:"memory"`
						EphemeralStorage string `yaml:"ephemeral-storage"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"checks"`
			HealthSync struct {
				Resources struct {
					Limits struct {
						Memory           string `yaml:"memory"`
						CPU              string `yaml:"cpu"`
						EphemeralStorage string `yaml:"ephemeral-storage"`
					} `yaml:"limits"`
					Requests struct {
						Memory string `yaml:"memory"`
						CPU    string `yaml:"cpu"`
					} `yaml:"requests"`
				} `yaml:"resources"`
				Sizing struct {
					BaseMemoryConsumption string `yaml:"baseMemoryConsumption"`
				} `yaml:"sizing"`
			} `yaml:"healthSync"`
			Initializer struct {
				Resources struct {
					Requests struct {
						CPU string `yaml:"cpu"`
					} `yaml:"requests"`
					Limits struct {
						EphemeralStorage string `yaml:"ephemeral-storage"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"initializer"`
			Notification struct {
				Resources struct {
					Requests struct {
						CPU    string `yaml:"cpu"`
						Memory string `yaml:"memory"`
					} `yaml:"requests"`
					Limits struct {
						CPU    string `yaml:"cpu"`
						Memory string `yaml:"memory"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"notification"`
			Router struct {
				Resources struct {
					Requests struct {
						CPU string `yaml:"cpu"`
					} `yaml:"requests"`
					Limits struct {
						CPU              string `yaml:"cpu"`
						EphemeralStorage string `yaml:"ephemeral-storage"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"router"`
			Slicing struct {
				Resources struct {
					Requests struct {
						CPU string `yaml:"cpu"`
					} `yaml:"requests"`
					Limits struct {
						EphemeralStorage string `yaml:"ephemeral-storage"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"slicing"`
			State struct {
				Resources struct {
					Limits struct {
						CPU              string `yaml:"cpu"`
						Memory           string `yaml:"memory"`
						EphemeralStorage string `yaml:"ephemeral-storage"`
					} `yaml:"limits"`
					Requests struct {
						CPU    string `yaml:"cpu"`
						Memory string `yaml:"memory"`
					} `yaml:"requests"`
				} `yaml:"resources"`
			} `yaml:"state"`
			Sync struct {
				TmpToPVC struct {
					VolumeSize string `yaml:"volumeSize"`
				} `yaml:"tmpToPVC"`
				Resources struct {
					Limits struct {
						CPU              string `yaml:"cpu"`
						Memory           string `yaml:"memory"`
						EphemeralStorage string `yaml:"ephemeral-storage"`
					} `yaml:"limits"`
					Requests struct {
						CPU    string `yaml:"cpu"`
						Memory string `yaml:"memory"`
					} `yaml:"requests"`
				} `yaml:"resources"`
			} `yaml:"sync"`
			E2Es struct {
				Resources struct {
					Requests struct {
						Memory string `yaml:"memory"`
						CPU    string `yaml:"cpu"`
					} `yaml:"requests"`
					Limits struct {
						Memory string `yaml:"memory"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"e2es"`
			Correlate struct {
				ReplicaCount int `yaml:"replicaCount"`
				Resources    struct {
					Limits struct {
						CPU    int    `yaml:"cpu"`
						Memory string `yaml:"memory"`
					} `yaml:"limits"`
					Requests struct {
						CPU              int    `yaml:"cpu"`
						Memory           string `yaml:"memory"`
						EphemeralStorage string `yaml:"ephemeral-storage"`
					} `yaml:"requests"`
				} `yaml:"resources"`
			} `yaml:"correlate"`
			Receiver struct {
				Split struct {
					Enabled bool `yaml:"enabled"`
					Base    struct {
						Resources struct {
							Requests struct {
								Memory string `yaml:"memory"`
								CPU    string `yaml:"cpu"`
							} `yaml:"requests"`
							Limits struct {
								Memory string `yaml:"memory"`
								CPU    string `yaml:"cpu"`
							} `yaml:"limits"`
						} `yaml:"resources"`
					} `yaml:"base"`
					ProcessAgent struct {
						Resources struct {
							Requests struct {
								Memory string `yaml:"memory"`
								CPU    string `yaml:"cpu"`
							} `yaml:"requests"`
							Limits struct {
								Memory string `yaml:"memory"`
								CPU    string `yaml:"cpu"`
							} `yaml:"limits"`
						} `yaml:"resources"`
					} `yaml:"processAgent"`
					Logs struct {
						Resources struct {
							Requests struct {
								Memory string `yaml:"memory"`
								CPU    string `yaml:"cpu"`
							} `yaml:"requests"`
							Limits struct {
								Memory string `yaml:"memory"`
								CPU    string `yaml:"cpu"`
							} `yaml:"limits"`
						} `yaml:"resources"`
					} `yaml:"logs"`
				} `yaml:"split"`
				ExtraEnv struct {
					Open struct {
						CONFIGFORCEAkkaHTTPHostConnectionPoolMaxOpenRequests string `yaml:"CONFIG_FORCE_akka_http_host__connection__pool_max__open__requests"`
					} `yaml:"open"`
				} `yaml:"extraEnv"`
			} `yaml:"receiver"`
			Vmagent struct {
				Resources struct {
					Limits struct {
						CPU    string `yaml:"cpu"`
						Memory string `yaml:"memory"`
					} `yaml:"limits"`
					Requests struct {
						CPU    string `yaml:"cpu"`
						Memory string `yaml:"memory"`
					} `yaml:"requests"`
				} `yaml:"resources"`
			} `yaml:"vmagent"`
			UI struct {
				ReplicaCount int `yaml:"replicaCount"`
			} `yaml:"ui"`
		} `yaml:"components"`
	} `yaml:"stackstate"`
	VictoriaMetrics0 struct {
		Server struct {
			Resources struct {
				Requests struct {
					CPU    int    `yaml:"cpu"`
					Memory string `yaml:"memory"`
				} `yaml:"requests"`
				Limits struct {
					CPU    int    `yaml:"cpu"`
					Memory string `yaml:"memory"`
				} `yaml:"limits"`
			} `yaml:"resources"`
		} `yaml:"server"`
		Backup struct {
			Vmbackup struct {
				Resources struct {
					Requests struct {
						Memory string `yaml:"memory"`
					} `yaml:"requests"`
					Limits struct {
						Memory string `yaml:"memory"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"vmbackup"`
		} `yaml:"backup"`
	} `yaml:"victoria-metrics-0"`
	VictoriaMetrics1 struct {
		Enabled bool `yaml:"enabled"`
		Server  struct {
			Resources struct {
				Requests struct {
					CPU    int    `yaml:"cpu"`
					Memory string `yaml:"memory"`
				} `yaml:"requests"`
				Limits struct {
					CPU    int    `yaml:"cpu"`
					Memory string `yaml:"memory"`
				} `yaml:"limits"`
			} `yaml:"resources"`
		} `yaml:"server"`
		Backup struct {
			Vmbackup struct {
				Resources struct {
					Requests struct {
						Memory string `yaml:"memory"`
					} `yaml:"requests"`
					Limits struct {
						Memory string `yaml:"memory"`
					} `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"vmbackup"`
		} `yaml:"backup"`
	} `yaml:"victoria-metrics-1"`
}

type IngressConfig struct {
	Ingress Ingress `yaml:"ingress"`
}

type Ingress struct {
	Enabled     bool              `yaml:"enabled"`
	Annotations map[string]string `yaml:"annotations"`
	Hosts       []Host            `yaml:"hosts"`
	TLS         []TLSConfig       `yaml:"tls"`
}

type Host struct {
	Host string `yaml:"host"`
}

type TLSConfig struct {
	Hosts      []string `yaml:"hosts"`
	SecretName string   `yaml:"secretName"`
}
