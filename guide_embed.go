package main

import _ "embed"

// userGuideMarkdown is the in-app user guide (also shipped as docs/user-guide.md).
// Embedded so the binary is self-contained.
//
//go:embed docs/user-guide.md
var userGuideMarkdown string
