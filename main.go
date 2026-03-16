// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	_ "time/tzdata" // embed IANA timezone data for portable builds

	"github.com/seckatie/glitchgate/cmd"
)

func main() {
	cmd.Execute()
}
