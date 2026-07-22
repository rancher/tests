module github.com/rancher/tests/actions

go 1.26.0

replace (
	github.com/containerd/containerd => github.com/containerd/containerd v1.6.27 // for compatibilty with docker 20.10.x
	github.com/crewjam/saml => github.com/rancher/saml v0.4.14-rancher3
	github.com/docker/distribution => github.com/docker/distribution v2.8.2+incompatible // rancher-machine requires a replace is set
	github.com/docker/docker => github.com/docker/docker v20.10.27+incompatible // rancher-machine requires a replace is set
	github.com/henrygd/beszel => github.com/longhorn/beszel v0.16.2-0.20260114090315-332709c32c7d

	github.com/rancher/rancher/pkg/apis => github.com/rancher/rancher/pkg/apis v0.0.0-20260527150105-ae26ccbc3fed
	github.com/rancher/rancher/pkg/client => github.com/rancher/rancher/pkg/client v0.0.0-20260527150105-ae26ccbc3fed
	github.com/rancher/tfp-automation => github.com/rancher/tfp-automation v0.0.0-20260715194347-e03e4ff5f114
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc => go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.53.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp => go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.53.0
	go.opentelemetry.io/otel => go.opentelemetry.io/otel v1.28.0
	go.opentelemetry.io/otel/metric => go.opentelemetry.io/otel/metric v1.28.0
	go.opentelemetry.io/otel/sdk => go.opentelemetry.io/otel/sdk v1.28.0
	go.opentelemetry.io/otel/trace => go.opentelemetry.io/otel/trace v1.28.0
	go.opentelemetry.io/proto/otlp => go.opentelemetry.io/proto/otlp v1.3.1

	helm.sh/helm/v3 => github.com/rancher/helm/v3 v3.16.1-rancher1
	k8s.io/api => k8s.io/api v0.36.1
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.36.1
	k8s.io/apimachinery => k8s.io/apimachinery v0.36.1
	k8s.io/apiserver => k8s.io/apiserver v0.36.1
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.36.1
	k8s.io/client-go => k8s.io/client-go v0.36.1
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.36.1
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.36.1
	k8s.io/code-generator => k8s.io/code-generator v0.36.1
	k8s.io/component-base => k8s.io/component-base v0.36.1
	k8s.io/component-helpers => k8s.io/component-helpers v0.36.1
	k8s.io/controller-manager => k8s.io/controller-manager v0.36.1
	k8s.io/cri-api => k8s.io/cri-api v0.36.1
	k8s.io/cri-client => k8s.io/cri-client v0.36.1
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.36.1
	k8s.io/dynamic-resource-allocation => k8s.io/dynamic-resource-allocation v0.36.1
	k8s.io/endpointslice => k8s.io/endpointslice v0.36.1
	k8s.io/externaljwt => k8s.io/externaljwt v0.36.1
	k8s.io/kms => k8s.io/kms v0.36.1
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.36.1
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.36.1
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20251125145642-4e65d59e963e
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.36.1
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.36.1
	k8s.io/kubectl => k8s.io/kubectl v0.36.1
	k8s.io/kubelet => k8s.io/kubelet v0.36.1
	k8s.io/kubernetes => k8s.io/kubernetes v1.36.1
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.36.1
	k8s.io/metrics => k8s.io/metrics v0.36.1
	k8s.io/mount-utils => k8s.io/mount-utils v0.36.1
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.36.1
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.36.1
	oras.land/oras-go => oras.land/oras-go v1.2.2 // for docker 20.10.x compatibility
	sigs.k8s.io/cluster-api => sigs.k8s.io/cluster-api v1.12.2
)

require (
	github.com/qase-tms/qase-go/qase-api-client v1.2.1
	github.com/rancher/rancher/pkg/apis v0.0.0
	github.com/rancher/shepherd v0.0.0-20260616224945-d2cbef93a360
	github.com/rancher/tfp-automation v0.0.0-20260715194347-e03e4ff5f114
)

require (
	github.com/aws/aws-sdk-go v1.55.8
	github.com/aws/aws-sdk-go-v2 v1.42.1
	github.com/aws/aws-sdk-go-v2/config v1.32.27
	github.com/aws/aws-sdk-go-v2/credentials v1.19.26
	github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager v0.2.13
	github.com/aws/aws-sdk-go-v2/service/s3 v1.104.2
	github.com/longhorn/longhorn-manager v1.12.0
	github.com/pkg/errors v0.9.1
	github.com/rancher/norman v0.9.6
	github.com/rancher/rancher v0.0.0-20260527150105-ae26ccbc3fed
	github.com/rancher/wrangler v1.1.2
	github.com/sirupsen/logrus v1.9.4
	github.com/stretchr/testify v1.11.1
	golang.org/x/crypto v0.53.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.36.2
	k8s.io/apimachinery v0.36.2
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/cluster-api v1.12.2
)

require (
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/rancher/ali-operator v1.14.0-rc.1 // indirect
	gopkg.in/validator.v2 v2.0.1 // indirect
	k8s.io/component-helpers v0.36.2 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.4.0 // indirect
)

require (
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/MakeNowJust/heredoc v1.0.0 // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.14 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.31 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.30 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.31 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.2.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.31.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.36.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.43.5 // indirect
	github.com/aws/smithy-go v1.27.3 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver v3.5.1+incompatible // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/c9s/goprocinfo v0.0.0-20210130143923-c95fcf8c64a8 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chai2010/gettext-go v1.0.2 // indirect
	github.com/cockroachdb/errors v1.14.0 // indirect
	github.com/cockroachdb/logtags v0.0.0-20241215232642-bb51bb14a506 // indirect
	github.com/cockroachdb/redact v1.1.8 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/creasty/defaults v1.5.2 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/distatus/battery v0.11.0 // indirect
	github.com/dolthub/maphash v0.1.0 // indirect
	github.com/ebitengine/purego v0.9.1 // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/evanphx/json-patch v5.9.11+incompatible // indirect
	github.com/exponent-io/jsonpath v0.0.0-20210407135951-1de76d718b3f // indirect
	github.com/fatih/camelcase v1.0.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
	github.com/getsentry/sentry-go v0.47.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/gliderlabs/ssh v0.3.8 // indirect
	github.com/go-errors/errors v1.4.2 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.23.1 // indirect
	github.com/go-openapi/jsonreference v0.21.6 // indirect
	github.com/go-openapi/swag v0.26.1 // indirect
	github.com/go-openapi/swag/cmdutils v0.26.1 // indirect
	github.com/go-openapi/swag/conv v0.26.1 // indirect
	github.com/go-openapi/swag/fileutils v0.26.1 // indirect
	github.com/go-openapi/swag/jsonname v0.26.1 // indirect
	github.com/go-openapi/swag/jsonutils v0.26.1 // indirect
	github.com/go-openapi/swag/loading v0.26.1 // indirect
	github.com/go-openapi/swag/mangling v0.26.1 // indirect
	github.com/go-openapi/swag/netutils v0.26.1 // indirect
	github.com/go-openapi/swag/stringutils v0.26.1 // indirect
	github.com/go-openapi/swag/typeutils v0.26.1 // indirect
	github.com/go-openapi/swag/yamlutils v0.26.1 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/gnostic-models v0.7.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/handlers v1.5.2 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/henrygd/beszel v0.18.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.18.2 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/kubereboot/kured v1.13.1 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/longhorn/go-common-libs v0.0.0-20260623123610-a0890c88e9f5 // indirect
	github.com/longhorn/go-spdk-helper v0.6.2 // indirect
	github.com/lufia/plan9stats v0.0.0-20251013123823-9fd1530e3ec3 // indirect
	github.com/lxzan/gws v1.8.9 // indirect
	github.com/mitchellh/go-ps v1.0.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/moby/spdystream v0.5.1 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pkg/sftp v1.13.5 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.72.0 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.69.0 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/rancher/aks-operator v1.15.0-rc.1 // indirect
	github.com/rancher/apiserver v0.9.6 // indirect
	github.com/rancher/eks-operator v1.15.0-rc.1 // indirect
	github.com/rancher/fleet/pkg/apis v0.15.0 // indirect
	github.com/rancher/gke-operator v1.15.0-rc.1 // indirect
	github.com/rancher/lasso v0.2.9 // indirect
	github.com/rancher/system-upgrade-controller/pkg/apis v0.0.0-20260519183600-f1362a3fe1a8 // indirect
	github.com/rancher/wrangler/v3 v3.7.0 // indirect
	github.com/robfig/cron v1.2.0 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/shirou/gopsutil/v3 v3.24.5 // indirect
	github.com/shirou/gopsutil/v4 v4.25.12 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/tklauser/go-sysconf v0.3.16 // indirect
	github.com/tklauser/numcpus v0.11.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xlab/treeprint v1.2.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/term v0.44.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.46.0 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	howett.net/plist v1.0.1 // indirect
	k8s.io/apiextensions-apiserver v0.36.2 // indirect
	k8s.io/apiserver v0.36.2 // indirect
	k8s.io/cli-runtime v0.36.2 // indirect
	k8s.io/component-base v0.36.2 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-aggregator v0.36.2 // indirect
	k8s.io/kube-openapi v0.31.5 // indirect
	k8s.io/kubectl v0.36.1 // indirect
	k8s.io/streaming v0.36.1 // indirect
	k8s.io/utils v0.0.0-20260617174310-a95e086a2553 // indirect
	sigs.k8s.io/cli-utils v0.37.2 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/kustomize/api v0.21.1 // indirect
	sigs.k8s.io/kustomize/kyaml v0.21.1 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
