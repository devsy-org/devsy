class Devsy < Formula
  desc "Standardized dev workspaces across Docker, Kubernetes, cloud, and SSH"
  homepage "https://www.devsy.sh"
  url "https://github.com/devsy-org/devsy/archive/refs/tags/v1.19.1.tar.gz"
  sha256 "REPLACE_WITH_V1_19_1_SOURCE_SHA256"
  license "MPL-2.0"

  depends_on "go" => :build

  def install
    ldflags = %W[
      -s -w
      -X github.com/devsy-org/devsy/pkg/version.version=v#{version}
      -X github.com/devsy-org/devsy/pkg/version.packageManager=homebrew
    ]
    system "go", "build", *std_go_args(ldflags:), "."
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/devsy --version")
    output = shell_output("#{bin}/devsy update 2>&1", 1)
    assert_match "brew upgrade devsy", output
  end
end
