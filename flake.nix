# Chat Nix Flake
# https://wiki.nixos.org/wiki/Flakes
{
  description = "Chat - Real-time matchmaking chat server";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs";
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      treefmt-nix,
    }:
    let
      supportedSystems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;

      # Derive the Go package attribute name from go.mod's `go X.Y[.Z]`
      # directive so the version only needs to be maintained in one place.
      goVersion =
        let
          mod = builtins.readFile ./go.mod;
          line = builtins.head (
            builtins.filter (l: builtins.match "^go [0-9].*" l != null) (nixpkgs.lib.splitString "\n" mod)
          );
          version = builtins.elemAt (builtins.split " " line) 2;
          parts = nixpkgs.lib.splitString "." version;
          major = builtins.elemAt parts 0;
          minor = builtins.elemAt parts 1;
        in
        "${major}_${minor}";

      treefmtEval = forAllSystems (
        system: treefmt-nix.lib.evalModule nixpkgs.legacyPackages.${system} ./treefmt.nix
      );
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          go = pkgs.${"go_${goVersion}"};
        in
        {
          default = self.packages.${system}.chat;
          chat = pkgs.callPackage ./nix/package.nix { inherit go; };
        }
      );

      apps = forAllSystems (system: {
        default = self.apps.${system}.chat;
        chat = {
          type = "app";
          program = "${self.packages.${system}.chat}/bin/chat";
          meta = self.packages.${system}.chat.meta;
        };
      });

      nixosModules = {
        default = self.nixosModules.chat;
        chat = import ./nix/module.nix self;
      };

      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          go = pkgs.${"go_${goVersion}"};
        in
        {
          default = pkgs.mkShell {
            packages = [
              go
              pkgs.gnumake
            ];
          };
        }
      );

      formatter = forAllSystems (system: treefmtEval.${system}.config.build.wrapper);

      checks = forAllSystems (system: {
        formatting = treefmtEval.${system}.config.build.check self;
      });
    };
}
