// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	_ "time/tzdata" // embed IANA timezone data for portable builds

	"codeberg.org/kglitchy/glitchgate/cmd"
)

func main() {
	cmd.Execute()
}
