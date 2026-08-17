module github.com/devsy-org/devsy

go 1.26.5

require (
	al.essio.dev/pkg/shellescape v1.6.0
	charm.land/huh/v2 v2.0.3
	charm.land/lipgloss/v2 v2.0.5
	filippo.io/age v1.3.1
	github.com/AlecAivazis/survey/v2 v2.3.7
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.22.0
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.14.0
	github.com/Microsoft/go-winio v0.6.3-0.20260107145400-75610162e7da
	github.com/awslabs/amazon-ecr-credential-helper/ecr-login v0.12.0
	github.com/blang/semver/v4 v4.0.0
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/charmbracelet/colorprofile v0.4.3
	github.com/charmbracelet/x/ansi v0.11.7
	github.com/compose-spec/compose-go/v2 v2.14.0
	github.com/containers/image/v5 v5.36.2
	github.com/creack/pty v1.1.24
	github.com/creativeprojects/go-selfupdate v1.6.0
	github.com/devsy-org/agentapi v1.0.1
	github.com/devsy-org/api v1.1.0
	github.com/devsy-org/apiserver v1.5.3
	github.com/devsy-org/ssh v1.2.2
	github.com/distribution/reference v0.6.0
	github.com/docker/cli v29.7.1+incompatible
	github.com/docker/docker v28.5.2+incompatible
	github.com/docker/docker-credential-helpers v0.9.8
	github.com/docker/go-connections v0.8.1
	github.com/evanphx/json-patch/v5 v5.9.11
	github.com/go-logr/logr v1.4.4
	github.com/go-logr/zapr v1.3.0
	github.com/gofrs/flock v0.13.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/go-cmp v0.7.0
	github.com/google/go-containerregistry v0.21.8
	github.com/google/go-containerregistry/pkg/authn/kubernetes v0.0.0-20260731170845-2ea098f4b134
	github.com/google/go-github/v90 v90.0.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/handlers v1.5.2
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674
	github.com/joho/godotenv v1.5.1
	github.com/moby/buildkit v0.32.2
	github.com/moby/patternmatcher v0.6.1
	github.com/moby/term v0.5.2
	github.com/modelcontextprotocol/go-sdk v1.7.0
	github.com/onsi/ginkgo/v2 v2.32.0
	github.com/onsi/gomega v1.42.1
	github.com/pkg/sftp v1.13.11
	github.com/posthog/posthog-go v1.22.0
	github.com/shirou/gopsutil/v4 v4.26.7
	github.com/skratchdot/open-golang v0.0.0-20200116055534-eef842397966
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/stretchr/testify v1.11.1
	github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f
	github.com/tidwall/gjson v1.19.0
	github.com/tonistiigi/fsutil v0.0.0-20260717003753-6d9dc2ebad62
	github.com/u-root/u-root v0.16.0
	github.com/zalando/go-keyring v0.2.8
	go.uber.org/atomic v1.11.0
	go.uber.org/goleak v1.3.0
	go.uber.org/zap v1.28.0
	golang.org/x/crypto v0.54.0
	golang.org/x/mod v0.38.0
	golang.org/x/sync v0.22.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
	google.golang.org/grpc v1.83.0
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af
	gopkg.in/yaml.v3 v3.0.1
	gotest.tools v2.2.0+incompatible
	k8s.io/api v0.36.3
	k8s.io/apimachinery v0.36.3
	k8s.io/client-go v0.36.3
	k8s.io/klog/v2 v2.140.0
	k8s.io/kube-aggregator v0.36.3
	k8s.io/kubectl v0.36.3
	k8s.io/streaming v0.36.3
	k8s.io/utils v0.0.0-20260707023825-cf1189d6abe3
	mvdan.cc/sh/v3 v3.13.1
	sigs.k8s.io/controller-runtime v0.24.1
	sigs.k8s.io/yaml v1.6.0
	tailscale.com v1.102.2
)

require (
	cel.dev/expr v0.25.2 // indirect
	charm.land/bubbles/v2 v2.0.0 // indirect
	charm.land/bubbletea/v2 v2.0.2 // indirect
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/auth v0.20.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	cloud.google.com/go/iam v1.11.0 // indirect
	cloud.google.com/go/kms v1.31.0 // indirect
	cloud.google.com/go/longrunning v1.0.0 // indirect
	cloud.google.com/go/monitoring v1.25.0 // indirect
	cloud.google.com/go/storage v1.62.2 // indirect
	code.gitea.io/sdk/gitea v0.25.1 // indirect
	dario.cat/mergo v1.0.2 // indirect
	filippo.io/edwards25519 v1.2.0 // indirect
	filippo.io/hpke v0.4.0 // indirect
	github.com/42wim/httpsig v1.2.4 // indirect
	github.com/AlekSi/pointer v1.2.0 // indirect
	github.com/Azure/azure-sdk-for-go v68.0.0+incompatible // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.12.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys v1.5.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/internal v1.2.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/storage/azblob v1.6.4 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/Azure/go-autorest v14.2.0+incompatible // indirect
	github.com/Azure/go-autorest/autorest v0.11.30 // indirect
	github.com/Azure/go-autorest/autorest/adal v0.9.24 // indirect
	github.com/Azure/go-autorest/autorest/azure/auth v0.5.13 // indirect
	github.com/Azure/go-autorest/autorest/azure/cli v0.4.7 // indirect
	github.com/Azure/go-autorest/autorest/date v0.3.1 // indirect
	github.com/Azure/go-autorest/logger v0.2.2 // indirect
	github.com/Azure/go-autorest/tracing v0.6.1 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.7.2 // indirect
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.33.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.55.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.55.0 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	github.com/Masterminds/sprig/v3 v3.3.0 // indirect
	github.com/NYTimes/gziphandler v1.1.1 // indirect
	github.com/ProtonMail/go-crypto v1.4.1 // indirect
	github.com/agext/levenshtein v1.2.3 // indirect
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/akutz/memconn v0.1.0 // indirect
	github.com/alexbrainman/sspi v0.0.0-20231016080023-1a75b4708caa // indirect
	github.com/anchore/go-macholibre v0.0.0-20250826193721-3cd206ca93aa // indirect
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/asaskevich/govalidator v0.0.0-20230301143203-a9d515a09cc2 // indirect
	github.com/atc0005/go-teams-notify/v2 v2.14.0 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/avast/retry-go/v4 v4.7.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.43.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.14 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.32.31 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.30 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.31 // indirect
	github.com/aws/aws-sdk-go-v2/feature/s3/manager v1.21.1 // indirect
	github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager v0.3.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.31 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.31 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.32 // indirect
	github.com/aws/aws-sdk-go-v2/service/ecr v1.56.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ecrpublic v1.38.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.24 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.31 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.32 // indirect
	github.com/aws/aws-sdk-go-v2/service/kms v1.52.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.106.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.0 // indirect
	github.com/aws/smithy-go v1.27.4 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blacktop/go-dwarf v1.0.14 // indirect
	github.com/blacktop/go-macho v1.1.263 // indirect
	github.com/blakesmith/ar v0.0.0-20190502131153-809d4375e1fb // indirect
	github.com/blang/semver v3.5.1+incompatible // indirect
	github.com/bluesky-social/indigo v0.0.0-20240813042137-4006c0eca043 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/caarlos0/env/v11 v11.4.1 // indirect
	github.com/caarlos0/go-reddit/v3 v3.0.1 // indirect
	github.com/caarlos0/go-version v0.2.2 // indirect
	github.com/caarlos0/log v0.6.2 // indirect
	github.com/carlmjohnson/versioninfo v0.22.5 // indirect
	github.com/catppuccin/go v0.3.0 // indirect
	github.com/cavaliergopher/cpio v1.0.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/fang v1.0.0 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260205113103-524a6607adb8 // indirect
	github.com/charmbracelet/x/exp/charmtone v0.0.0-20250603201427-c31516f43444 // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/exp/strings v0.0.0-20240722160745-212f7b056ed0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/chrismellard/docker-credential-acr-env v0.0.0-20230304212654-82a0ddb27589 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/cncf/xds/go v0.0.0-20260202195803-dba9d589def2 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/containerd/console v1.0.5 // indirect
	github.com/containerd/containerd/api v1.11.1 // indirect
	github.com/containerd/containerd/v2 v2.3.3 // indirect
	github.com/containerd/continuity v0.5.0 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/platforms v1.0.0-rc.4 // indirect
	github.com/containerd/ttrpc v1.2.9 // indirect
	github.com/containerd/typeurl/v2 v2.3.0 // indirect
	github.com/containers/storage v1.59.1 // indirect
	github.com/coreos/go-oidc/v3 v3.20.0 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/creachadair/msync v0.8.1 // indirect
	github.com/cyberphone/json-canonicalization v0.0.0-20241213102144-19d51d7fe467 // indirect
	github.com/cyphar/filepath-securejoin v0.6.1 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/davidmz/go-pageant v1.0.2 // indirect
	github.com/dblohm7/wingoes v0.0.0-20240119213807-a09d6be7affa // indirect
	github.com/devsy-org/admin-apis v1.2.0 // indirect
	github.com/dghubble/go-twitter v0.0.0-20211115160449-93a8679adecb // indirect
	github.com/dghubble/oauth1 v0.7.3 // indirect
	github.com/dghubble/sling v1.4.0 // indirect
	github.com/digitorus/pkcs7 v0.0.0-20250730155240-ffadbf3f398c // indirect
	github.com/digitorus/timestamp v0.0.0-20250524132541-c45532741eea // indirect
	github.com/dimchansky/utfbom v1.1.1 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.10.2 // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.37.0 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.3.3 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/gaissmai/bart v0.26.1 // indirect
	github.com/github/smimesign v0.2.0 // indirect
	github.com/go-fed/httpsig v1.1.0 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.9.0 // indirect
	github.com/go-git/go-git/v5 v5.19.1 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/analysis v0.25.2 // indirect
	github.com/go-openapi/errors v0.22.8 // indirect
	github.com/go-openapi/jsonpointer v0.23.1 // indirect
	github.com/go-openapi/jsonreference v0.21.6 // indirect
	github.com/go-openapi/loads v0.24.0 // indirect
	github.com/go-openapi/runtime v0.32.4 // indirect
	github.com/go-openapi/runtime/server-middleware v0.30.0 // indirect
	github.com/go-openapi/spec v0.22.6 // indirect
	github.com/go-openapi/strfmt v0.26.4 // indirect
	github.com/go-openapi/swag v0.26.1 // indirect
	github.com/go-openapi/swag/cmdutils v0.26.1 // indirect
	github.com/go-openapi/swag/conv v0.27.0 // indirect
	github.com/go-openapi/swag/fileutils v0.26.1 // indirect
	github.com/go-openapi/swag/jsonname v0.26.1 // indirect
	github.com/go-openapi/swag/jsonutils v0.26.1 // indirect
	github.com/go-openapi/swag/loading v0.26.1 // indirect
	github.com/go-openapi/swag/mangling v0.26.1 // indirect
	github.com/go-openapi/swag/netutils v0.26.1 // indirect
	github.com/go-openapi/swag/stringutils v0.26.1 // indirect
	github.com/go-openapi/swag/typeutils v0.27.0 // indirect
	github.com/go-openapi/swag/yamlutils v0.26.1 // indirect
	github.com/go-openapi/validate v0.26.0 // indirect
	github.com/go-restruct/restruct v1.2.0-alpha // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/cel-go v0.27.0 // indirect
	github.com/google/certificate-transparency-go v1.3.3 // indirect
	github.com/google/gnostic-models v0.7.1 // indirect
	github.com/google/go-github/v86 v86.0.0 // indirect
	github.com/google/go-github/v89 v89.0.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/ko v0.19.1 // indirect
	github.com/google/pprof v0.0.0-20260402051712-545e8a4df936 // indirect
	github.com/google/rpmpack v0.7.1 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/google/wire v0.7.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.16 // indirect
	github.com/googleapis/gax-go/v2 v2.22.0 // indirect
	github.com/goreleaser/chglog v0.7.4 // indirect
	github.com/goreleaser/fileglob v1.4.0 // indirect
	github.com/goreleaser/go-shellwords v1.0.13 // indirect
	github.com/goreleaser/goreleaser/v2 v2.17.1 // indirect
	github.com/goreleaser/nfpm/v2 v2.47.0 // indirect
	github.com/goreleaser/quill v0.0.0-20260630015114-8310f3e9a321 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus v1.1.0 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.3.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/hdevalence/ed25519consensus v0.2.0 // indirect
	github.com/huandu/xstrings v1.5.0 // indirect
	github.com/huin/goupnp v1.3.0 // indirect
	github.com/in-toto/attestation v1.2.0 // indirect
	github.com/in-toto/in-toto-golang v0.11.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/ipfs/bbloom v0.0.4 // indirect
	github.com/ipfs/go-block-format v0.2.0 // indirect
	github.com/ipfs/go-cid v0.4.1 // indirect
	github.com/ipfs/go-datastore v0.6.0 // indirect
	github.com/ipfs/go-ipfs-blockstore v1.3.1 // indirect
	github.com/ipfs/go-ipfs-ds-help v1.1.1 // indirect
	github.com/ipfs/go-ipfs-util v0.0.3 // indirect
	github.com/ipfs/go-ipld-cbor v0.1.0 // indirect
	github.com/ipfs/go-ipld-format v0.6.0 // indirect
	github.com/ipfs/go-log v1.0.5 // indirect
	github.com/ipfs/go-log/v2 v2.5.1 // indirect
	github.com/ipfs/go-metrics-interface v0.0.1 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/jbenet/goprocess v0.1.4 // indirect
	github.com/jedisct1/go-minisign v0.0.0-20241212093149-d2f9f49435c7 // indirect
	github.com/jsimonetti/rtnetlink v1.4.1 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/klauspost/pgzip v1.2.6 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.23 // indirect
	github.com/mattn/go-mastodon v0.0.13 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/mattn/go-shellwords v1.0.12 // indirect
	github.com/mdlayher/netlink v1.7.3-0.20250113171957-fbb4dce95f42 // indirect
	github.com/mdlayher/socket v0.5.1 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/minio/sha256-simd v1.0.1 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/go-ps v1.0.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/mitchellh/hashstructure/v2 v2.0.2 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/locker v1.0.1 // indirect
	github.com/moby/moby/api v1.55.0 // indirect
	github.com/moby/moby/client v0.5.1 // indirect
	github.com/moby/spdystream v0.5.1 // indirect
	github.com/moby/sys/capability v0.4.0 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/moby/sys/signal v0.7.1 // indirect
	github.com/moby/sys/user v0.4.1 // indirect
	github.com/modelcontextprotocol/registry v1.8.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/morikuni/aec v1.1.0 // indirect
	github.com/mr-tron/base58 v1.2.0 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/mango v0.2.0 // indirect
	github.com/muesli/mango-cobra v1.3.0 // indirect
	github.com/muesli/mango-pflag v0.1.0 // indirect
	github.com/muesli/roff v0.1.0 // indirect
	github.com/multiformats/go-base32 v0.1.0 // indirect
	github.com/multiformats/go-base36 v0.2.0 // indirect
	github.com/multiformats/go-multibase v0.2.0 // indirect
	github.com/multiformats/go-multihash v0.2.3 // indirect
	github.com/multiformats/go-varint v0.0.7 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/oklog/ulid/v2 v2.1.1 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/opencontainers/runtime-spec v1.3.0 // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/pelletier/go-toml/v2 v2.3.1 // indirect
	github.com/pires/go-proxyproto v0.8.1 // indirect
	github.com/pjbgf/sha1cd v0.6.0 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/polydawn/refmt v0.89.1-0.20221221234430-40501e09de1f // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.69.0 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/safchain/ethtool v0.3.0 // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v5 v5.3.1 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/sassoftware/relic v7.2.1+incompatible // indirect
	github.com/scylladb/go-set v1.0.3-0.20200225121959-cc7b2070d91e // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.11.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sergi/go-diff v1.4.0 // indirect
	github.com/shibumi/go-pathspec v1.3.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/sigstore/cosign/v3 v3.1.1 // indirect
	github.com/sigstore/protobuf-specs v0.5.1 // indirect
	github.com/sigstore/rekor v1.5.3 // indirect
	github.com/sigstore/rekor-tiles/v2 v2.3.0 // indirect
	github.com/sigstore/sigstore v1.10.8 // indirect
	github.com/sigstore/sigstore-go v1.2.2 // indirect
	github.com/sigstore/timestamp-authority/v2 v2.1.2 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/skeema/knownhosts v1.3.1 // indirect
	github.com/slack-go/slack v0.27.0 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/spiffe/go-spiffe/v2 v2.7.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/tailscale/certstore v0.1.1-0.20260409135935-3638fb84b77d // indirect
	github.com/tailscale/go-winio v0.0.0-20231025203758-c4f33415bf55 // indirect
	github.com/tailscale/peercred v0.0.0-20250107143737-35a0c7bd7edc // indirect
	github.com/tailscale/web-client-prebuilt v0.0.0-20250124233751-d4cd19a26976 // indirect
	github.com/tailscale/wireguard-go v0.0.0-20260730222847-4affce44577c // indirect
	github.com/theupdateframework/go-tuf v0.7.0 // indirect
	github.com/theupdateframework/go-tuf/v2 v2.4.2 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tklauser/go-sysconf v0.3.16 // indirect
	github.com/tklauser/numcpus v0.11.0 // indirect
	github.com/tomnomnom/linkheader v0.0.0-20250811210735-e5fe3b51442e // indirect
	github.com/tonistiigi/go-csvvalue v0.0.0-20240814133006-030d3b2625d0 // indirect
	github.com/tonistiigi/units v0.0.0-20180711220420-6950e57a87ea // indirect
	github.com/tonistiigi/vt100 v0.0.0-20240514184818-90bafcd6abab // indirect
	github.com/transparency-dev/formats v0.1.1 // indirect
	github.com/transparency-dev/merkle v0.0.2 // indirect
	github.com/ulikunitz/xz v0.5.16 // indirect
	github.com/wagoodman/go-progress v0.0.0-20230925121702-07e42b3cdba0 // indirect
	github.com/whyrusleeping/cbor-gen v0.1.3-0.20240731173018-74d74643234c // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/xhit/go-str2duration/v2 v2.1.0 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	gitlab.com/digitalxero/go-conventional-commit v1.0.7 // indirect
	gitlab.com/gitlab-org/api/client-go v1.46.0 // indirect
	go.digitalxero.dev/go-msix v0.3.1 // indirect
	go.etcd.io/etcd/api/v3 v3.6.8 // indirect
	go.etcd.io/etcd/client/pkg/v3 v3.6.8 // indirect
	go.etcd.io/etcd/client/v3 v3.6.8 // indirect
	go.mozilla.org/pkcs7 v0.9.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/detectors/gcp v1.44.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.69.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace v0.69.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.6 // indirect
	go4.org/mem v0.0.0-20240501181205-ae6ca9944745 // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	gocloud.dev v0.46.0 // indirect
	golang.org/x/exp v0.0.0-20260603202125-055de637280b // indirect
	golang.org/x/net v0.57.1-0.20260729233039-99c3b0a8f463 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/telemetry v0.0.0-20260708182218-49f421fb7959 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	golang.zx2c4.com/wireguard/windows v0.5.3 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/api v0.283.0 // indirect
	google.golang.org/genproto v0.0.0-20260406210006-6f92a3bedf2d // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.6.1 // indirect
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/mail.v2 v2.3.1 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	gvisor.dev/gvisor v0.0.0-20260224225140-573d5e7127a8 // indirect
	k8s.io/apiextensions-apiserver v0.36.2 // indirect
	k8s.io/apiserver v0.36.3 // indirect
	k8s.io/cli-runtime v0.36.3 // indirect
	k8s.io/component-base v0.36.3 // indirect
	k8s.io/kube-openapi v0.0.0-20260624041617-8f3fa4921821 // indirect
	k8s.io/metrics v0.36.3 // indirect
	lukechampine.com/blake3 v1.4.1 // indirect
	sigs.k8s.io/apiserver-network-proxy/konnectivity-client v0.34.0 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/kind v0.32.0 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.3 // indirect
	software.sslmate.com/src/go-pkcs12 v0.7.3 // indirect
)

replace tailscale.com => github.com/devsy-org/tailscale v1.102.2

tool (
	github.com/goreleaser/goreleaser/v2
	github.com/onsi/ginkgo/v2/ginkgo
	golang.org/x/tools/cmd/goimports
	google.golang.org/grpc/cmd/protoc-gen-go-grpc
	google.golang.org/protobuf/cmd/protoc-gen-go
)
