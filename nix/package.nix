# Chat package derivation
# https://ryantm.github.io/nixpkgs/languages-frameworks/go/
{
  lib,
  go,
  buildGoModule,
}:
(buildGoModule.override { inherit go; }) {
  pname = "chat";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../cmd
      ../internal
    ];
  };

  proxyVendor = true;

  # Run `nix build` once to get the correct hash, then update this value
  vendorHash = "sha256-JGlEh2wK3346jvMzxRvb4Q3iozTk3IxNN0HH7dfY8VE=";

  subPackages = [ "cmd/chat" ];

  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
  ];

  meta = {
    description = "Real-time matchmaking chat server";
    homepage = "https://github.com/stolasapp/chat";
    license = lib.licenses.asl20;
    mainProgram = "chat";
  };
}
