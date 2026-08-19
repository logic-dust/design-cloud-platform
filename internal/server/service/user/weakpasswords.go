package user

import (
	"bufio"
	"embed"
	"strings"
)

//go:embed weakpasswords.txt
var weakPasswordsData embed.FS

func isWeakPassword(password string) bool {
	data, err := weakPasswordsData.ReadFile("weakpasswords.txt")
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		if scanner.Text() == password {
			return true
		}
	}
	return false
}
