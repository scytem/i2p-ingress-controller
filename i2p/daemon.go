package i2p

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

type I2p struct {
	cmd *exec.Cmd
}

func (i *I2p) Start() {
	fmt.Println("starting i2pd...")

	i.cmd = exec.Command("i2pd", "--tunconf", "/var/lib/i2pd/tunnels.conf")
	i.cmd.Stdout = os.Stdout
	i.cmd.Stderr = os.Stderr

	err := i.cmd.Start()
	if err != nil {
		fmt.Print(err)
		return
	}
}

func (i *I2p) Reload() {
	fmt.Println("reloading i2pd...")

	i.cmd.Process.Signal(syscall.SIGTERM)
	i.cmd.Process.Wait()
	i.Start()
}
