// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

/*
 * This file is used to plug in modules that can be used by dutagent.
 * By importing the modules here, the modules are automatically registered.
 * The modules are registered by the init function in the module package.
 * The init function of a module package must call the module.Register() function.
 */

import (
	_ "github.com/BlindspotSoftware/dutctl/pkg/module/agent"
	_ "github.com/BlindspotSoftware/dutctl/pkg/module/dummy"
	_ "github.com/BlindspotSoftware/dutctl/pkg/module/gpio"
	_ "github.com/BlindspotSoftware/dutctl/pkg/module/serial"
	_ "github.com/BlindspotSoftware/dutctl/pkg/module/shell"
	_ "github.com/BlindspotSoftware/dutctl/pkg/module/ssh"
	_ "github.com/BlindspotSoftware/dutctl/pkg/module/time"
)
