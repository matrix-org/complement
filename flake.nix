{
  description = "A Golang development environment";

  outputs = {
    self,
    nixpkgs,
    flake-utils,
  }:
    flake-utils.lib.eachDefaultSystem
    (system: let
      pkgs = import nixpkgs {
        inherit system;
      };
    in {
      devShells.default = pkgs.mkShell {
        buildInputs = with pkgs; [
          # Native dependencies needed by Complement
          olm

          go

          # Go language server
          gopls

          # Go debugger
          delve

          # Tools dependended on by VSCode Go Extension
          gomodifytags
          gotests
          impl
          go-outline
          go-tools # includes `staticcheck`
        ];
      };
    });
}
