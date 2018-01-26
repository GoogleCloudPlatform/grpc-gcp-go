package userutil

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ReadFromConsole ... Read user input from console
func ReadFromConsole() string {
	reader := bufio.NewReader(os.Stdin)
	text, err := reader.ReadString('\n')
	text = strings.TrimSpace(text)

	if err != nil {
		fmt.Println(err)
		return ""
	}
	return text

}
