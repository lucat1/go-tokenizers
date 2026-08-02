let
  pkgs = import <nixpkgs> {};
in pkgs.mkShell {
  buildInputs = with pkgs; [
    go
    gopls

    rustup
  ];

  shellHook = ''
    export LIBRARY_PATH=${pkgs.llvmPackages.libcxx}/lib
  '';
}
