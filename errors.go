package main

import "errors"

var (
	ErrSmokeNoTarballs     = errors.New("smoke: no tarballs")
	ErrHelloOutput         = errors.New("hello output unexpected")
	ErrUtilVersion         = errors.New("utility version failed without LD_LIBRARY_PATH")
	ErrMissingClang        = errors.New("missing bin/clang")
	ErrUnsupportedTarget   = errors.New("unsupported linux target")
	ErrCloneTrunk          = errors.New("clone trunk failed")
	ErrClangMissingInstall = errors.New("clang missing after install")
	ErrMSVCNotOnPATH       = errors.New("cl.exe not on PATH; run from VS dev shell or GHA msvc-dev-cmd")
)
