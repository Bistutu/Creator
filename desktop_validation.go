package main

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/profile"
)

type formField uint8

const (
	noField formField = iota
	addressField
	nameField
	widthField
	heightField
)

type formIssue struct {
	field   formField
	message string
}

func (issue *formIssue) fieldChanged(field formField) {
	if issue.field == field || issue.field == noField {
		*issue = formIssue{}
	}
}

func (issue formIssue) draw(c *ui.Context, input ui.Element) ui.Element {
	if issue.message == "" {
		return ui.Element{}
	}
	danger := c.Theme().Danger
	message := ui.Text(c.Key("issue"), issue.message).FontSize(12).TextColor(danger).Role(ui.RoleStatus).Wrap()
	if input.Valid() {
		input.Invalid(true).ErrorMessage(message).Border(1, danger).FocusRingColor(danger)
	}
	return message
}

func readWindowSize(widthText, heightText string) (int, int, formIssue) {
	width, widthErr := strconv.Atoi(strings.TrimSpace(widthText))
	height, heightErr := strconv.Atoi(strings.TrimSpace(heightText))
	issue := profile.ValidateWindowSize(width, height)
	const invalidNumber = "请输入有效的窗口尺寸"
	switch {
	case widthErr != nil:
		return 0, 0, formIssue{widthField, invalidNumber}
	case issue == profile.ErrWindowWidth:
		return 0, 0, formIssue{widthField, issue.Error()}
	case heightErr != nil:
		return 0, 0, formIssue{heightField, invalidNumber}
	case issue != nil:
		return 0, 0, formIssue{heightField, issue.Error()}
	default:
		return width, height, formIssue{}
	}
}
