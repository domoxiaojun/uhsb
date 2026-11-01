package cfcommon

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PromptString 读取一行输入,空则返默认值。
func PromptString(prompt, defaultVal string) string {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

// PromptInt 读整数输入,空或无效返默认值。
func PromptInt(prompt string, defaultVal int) int {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(line)
	if err != nil {
		return defaultVal
	}
	return v
}

// PromptChoice 在 valid 列表中选一个(大小写不敏感),空则返默认值,无效则重试。
func PromptChoice(prompt string, valid []string, defaultVal string) string {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(prompt)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return defaultVal
		}
		for _, v := range valid {
			if strings.EqualFold(line, v) {
				return v
			}
		}
		fmt.Println("输入无效,请重试。")
	}
}
