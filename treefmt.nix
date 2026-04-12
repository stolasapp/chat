# treefmt.nix
# https://github.com/numtide/treefmt-nix
{ ... }:
{
  projectRootFile = "flake.nix";
  programs.nixfmt.enable = true;
}
