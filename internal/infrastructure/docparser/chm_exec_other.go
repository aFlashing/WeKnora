//go:build !windows

package docparser

import "os/exec"

// hideCHMConvertWindow is a no-op outside Windows, where a CLI build has no
// window to hide.
func hideCHMConvertWindow(*exec.Cmd) {}
