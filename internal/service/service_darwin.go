//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }
func loaded(root string) bool {
	return exec.Command("/bin/launchctl", "print", domain()+"/"+Label(root)).Run() == nil
}
func IsLoaded(root string) bool { return loaded(root) }
func launch(args ...string) error {
	if err := exec.Command("/bin/launchctl", args...).Run(); err != nil {
		return fmt.Errorf("launchctl failed: %w", err)
	}
	return nil
}
func Start(home, root string) error {
	if !loaded(root) {
		if err := launch("bootstrap", domain(), Path(home, root)); err != nil {
			return err
		}
	}
	return launch("kickstart", domain()+"/"+Label(root))
}
func Stop(root string) error {
	if !loaded(root) {
		return nil
	}
	return launch("bootout", domain()+"/"+Label(root))
}
