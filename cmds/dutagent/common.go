// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
)

func findCmd(list dut.Devlist, wantDev, wantCmd string) (dut.Device, dut.Command, error) {
	var (
		dev dut.Device
		cmd dut.Command
	)

	dev, ok := list[wantDev]
	if !ok {
		e := connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("device %q does not exist", wantDev))

		return dev, cmd, e
	}

	cmd, ok = dev.Cmds[wantCmd]
	if !ok {
		e := connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("device %q does not have command %q", wantDev, wantCmd))

		return dev, cmd, e
	}

	if len(cmd.Modules) == 0 {
		e := connect.NewError(connect.CodeInternal, fmt.Errorf("no modules set for command %q at device %q", wantCmd, wantDev))

		return dev, cmd, e
	}

	return dev, cmd, nil
}
