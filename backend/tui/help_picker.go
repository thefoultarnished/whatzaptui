package main

var helpCommands = []struct {
	cmd  string
	desc string
}{
	{"/help", "Show this help"},
	{"/theme", "Change color theme"},
	{"/pointer", "Change message icon"},
	{"/emoji", "Open emoji picker"},
	{"/rename", "Rename a contact"},
	{"/whitelist", "Allow a contact"},
	{"/whitelistall", "Allow all contacts"},
	{"/blacklist", "Block a contact"},
	{"/blacklistall", "Block all contacts"},
	{"/block", "Block contact on WhatsApp"},
	{"/synccontacts", "Sync contacts from WhatsApp"},
	{"/syncgroups", "Sync groups from WhatsApp"},
	{"/mouseon", "Enable mouse support"},
	{"/mouseoff", "Disable mouse support"},
	{"/soundon", "Enable notification sounds"},
	{"/soundoff", "Disable notification sounds"},
	{"/sound1", "Sound profile 1"},
	{"/sound2", "Sound profile 2"},
	{"/sound3", "Sound profile 3"},
	{"/sound4", "Sound profile 4"},
	{"/sound5", "Sound profile 5"},
	{"/logout", "Log out of WhatsApp"},
	{"/restart", "Restart the app"},
	{"/exit", "Exit the app"},
}

func buildHelpPickerItems() []pickerItem {
	items := make([]pickerItem, len(helpCommands))
	for i, c := range helpCommands {
		items[i] = pickerItem{key: c.cmd, label: c.cmd + "  " + c.desc}
	}
	return items
}
