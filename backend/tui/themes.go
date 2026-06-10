package main

var (
	// Ember — near-black coal bg, glowing amber/orange/red — like a dying fire
	Ember = Theme{
		Brand:                 "#ff8c00", // amber flame
		Accent:                "#ff6b35", // vivid ember orange
		Purple:                "#cc3333", // deep red — no purple in fire
		Amber:                 "#ffb347", // bright amber
		Red:                   "#ff3300", // hot red ember
		Muted:                 "#4a3018", // dark coal-brown
		Text:                  "#f5deb3", // warm wheat, fire-lit parchment
		ImageTag:              "#ff4500", // orange-red
		VideoTag:              "#ffb347", // amber
		AudioTag:              "#ffd700", // molten gold
		FileTag:               "#ff8c00", // deep amber
		StickerTag:            "#cc3333", // deep red
		ContactTag:            "#e06030", // burnt orange
		PollTag:               "#ffcb6b", // warm gold
		LocationTag:           "#ff6b35", // ember orange
		AnomalyTag:            "#ff3300", // hot red
		SentText:              "#ffe0b0", // warm amber-white
		ReceivedText:          "#ffd0a0", // warm parchment
		SentName:              "#ff8c00", // amber flame — "me"
		ReceivedName:          "#cc3333", // deep red — "them"
		QuotedSentText:        "#a07040", // muted amber
		QuotedReceivedText:    "#9a5a30", // muted burnt orange
		BadgeInk:              "#0e0900",
		ButtonInk:             "#0e0900",
		TagInk:                "#0e0900",
		Cursor:                "#ff8c00",
		QRLight:               "#FFFFFF",
		QRDark:                "#0e0900",
		ShortcutActive:        "#1e1400",
		SidebarActiveBg:       "#2a1800", // dark amber tint
		SidebarActiveUnreadBg: "#251000", // darker red tint
		SidebarWhitelistActiveBg: "#ff8c00", // brand amber
		SidebarBlacklistActiveBg: "#ff3300", // red
		ReplyPreviewBg:        "#1a1000",
		MessageSelectedBg:     "#1e1200",
		MediaTokenBg:          "#ff4500",
		MediaTokenPulseBg:     "#ff8c00",
		Background:            "#0e0900", // near-black coal
	}

	// Glacier — very dark ice-navy bg, pale blues and crisp whites — cold and minimal
	Glacier = Theme{
		Brand:                 "#7ec8e3", // glacier blue
		Accent:                "#a8daf0", // pale ice
		Purple:                "#8899cc", // deep periwinkle
		Amber:                 "#b8d4e8", // pale cool — no warm amber in glaciers
		Red:                   "#7090b8", // steel-blue danger
		Muted:                 "#2a4060", // deep slate
		Text:                  "#d8eef8", // cool ice-white
		ImageTag:              "#a8daf0", // ice blue
		VideoTag:              "#7ec8e3", // glacier
		AudioTag:              "#c4e8f8", // very pale ice
		FileTag:               "#8ab8d8", // medium ice
		StickerTag:            "#8899cc", // periwinkle
		ContactTag:            "#60b0d0", // teal-blue
		PollTag:               "#b0d0e8", // soft ice
		LocationTag:           "#7890c0", // deep periwinkle
		AnomalyTag:            "#9090b8", // muted cool — cold anomaly
		SentText:              "#c0e8f8", // icy sent
		ReceivedText:          "#d0d8f0", // periwinkle-tinted received
		SentName:              "#7ec8e3", // glacier — "me"
		ReceivedName:          "#8899cc", // periwinkle — "them"
		QuotedSentText:        "#5888a8", // deeper glacier
		QuotedReceivedText:    "#607898", // muted slate
		BadgeInk:              "#080e14",
		ButtonInk:             "#080e14",
		TagInk:                "#080e14",
		Cursor:                "#a8daf0",
		QRLight:               "#FFFFFF",
		QRDark:                "#080e14",
		ShortcutActive:        "#101c28",
		SidebarActiveBg:       "#142030", // dark ice-navy active
		SidebarActiveUnreadBg: "#0e1a2a", // deeper navy unread
		SidebarWhitelistActiveBg: "#7ec8e3", // brand blue
		SidebarBlacklistActiveBg: "#7090b8", // red
		ReplyPreviewBg:        "#0c1824",
		MessageSelectedBg:     "#101e2c",
		MediaTokenBg:          "#7ec8e3",
		MediaTokenPulseBg:     "#a8daf0",
		Background:            "#080e14", // very dark ice-navy
	}

	// Verdant — very dark forest floor bg, rich greens and warm bark tones
	Verdant = Theme{
		Brand:                 "#5aab3a", // forest green
		Accent:                "#78c850", // leaf bright green
		Purple:                "#8a9060", // olive — nature's "purple"
		Amber:                 "#c8a030", // honey gold — bark warmth
		Red:                   "#c04a3a", // berry red
		Muted:                 "#3a4828", // dark olive
		Text:                  "#c8dca8", // pale sage
		ImageTag:              "#c04a3a", // berry
		VideoTag:              "#78c850", // leaf
		AudioTag:              "#c8a030", // honey
		FileTag:               "#5aab3a", // forest
		StickerTag:            "#8a9060", // olive
		ContactTag:            "#50a860", // teal-green
		PollTag:               "#d4b844", // warm gold
		LocationTag:           "#6a9060", // sage
		AnomalyTag:            "#c04a3a", // berry
		SentText:              "#c0e8a0", // pale leaf green
		ReceivedText:          "#d4c890", // warm parchment — earthy contrast
		SentName:              "#5aab3a", // forest green — "me"
		ReceivedName:          "#c8a030", // honey gold — "them"
		QuotedSentText:        "#7a9860", // muted olive-green
		QuotedReceivedText:    "#9a8850", // muted bark
		BadgeInk:              "#0a0f07",
		ButtonInk:             "#0a0f07",
		TagInk:                "#0a0f07",
		Cursor:                "#78c850",
		QRLight:               "#FFFFFF",
		QRDark:                "#0a0f07",
		ShortcutActive:        "#141e0c",
		SidebarActiveBg:       "#182410", // dark forest active
		SidebarActiveUnreadBg: "#1a1e08", // darker earthy unread
		SidebarWhitelistActiveBg: "#5aab3a", // brand green
		SidebarBlacklistActiveBg: "#c04a3a", // red
		ReplyPreviewBg:        "#121a0c",
		MessageSelectedBg:     "#101808",
		MediaTokenBg:          "#5aab3a",
		MediaTokenPulseBg:     "#78c850",
		Background:            "#0a0f07", // very dark forest floor
	}

	// Dusk — deep indigo-purple bg, warm sunset oranges, rose-golds, twilight violet
	Dusk = Theme{
		Brand:                 "#f4a261", // warm sunset orange-gold
		Accent:                "#e76f51", // burnt coral — the last light
		Purple:                "#c084fc", // twilight violet
		Amber:                 "#ffd166", // golden hour
		Red:                   "#ef4565", // vivid sunset red
		Muted:                 "#5a3868", // deep indigo-purple
		Text:                  "#f0d4e8", // warm lavender-rose white
		ImageTag:              "#ef4565", // sunset red
		VideoTag:              "#c084fc", // violet
		AudioTag:              "#ffd166", // golden
		FileTag:               "#f4a261", // orange-gold
		StickerTag:            "#e879f9", // orchid
		ContactTag:            "#ff9eb5", // rose
		PollTag:               "#ffd166", // gold
		LocationTag:           "#a08cff", // periwinkle-violet
		AnomalyTag:            "#ef4565", // sunset red
		SentText:              "#ffe8c8", // warm golden-white — sunset light
		ReceivedText:          "#f0dff8", // soft violet — twilight sky
		SentName:              "#f4a261", // sunset orange — "me"
		ReceivedName:          "#c084fc", // twilight violet — "them"
		QuotedSentText:        "#a07858", // muted amber-gold
		QuotedReceivedText:    "#a080b8", // muted violet
		BadgeInk:              "#110818",
		ButtonInk:             "#110818",
		TagInk:                "#110818",
		Cursor:                "#f4a261",
		QRLight:               "#FFFFFF",
		QRDark:                "#110818",
		ShortcutActive:        "#1e1028",
		SidebarActiveBg:       "#2a1040", // rich plum-indigo active
		SidebarActiveUnreadBg: "#1e0c30", // deeper indigo unread
		SidebarWhitelistActiveBg: "#f4a261", // brand orange
		SidebarBlacklistActiveBg: "#ef4565", // red
		ReplyPreviewBg:        "#1c0c30",
		MessageSelectedBg:     "#180a28",
		MediaTokenBg:          "#e76f51",
		MediaTokenPulseBg:     "#f4a261",
		Background:            "#110818", // deep indigo-purple
	}

	// Fossil — dark warm sepia-black bg, aged parchment, ochre, leather tones
	Fossil = Theme{
		Brand:                 "#c8a864", // parchment gold
		Accent:                "#a87c50", // aged leather
		Purple:                "#8a6858", // muted terracotta — earthy "purple"
		Amber:                 "#d4b06a", // warm ochre
		Red:                   "#a84840", // burnt sienna
		Muted:                 "#604e38", // dark sepia
		Text:                  "#e8dcc0", // aged parchment
		ImageTag:              "#a84840", // burnt sienna
		VideoTag:              "#a87c50", // leather
		AudioTag:              "#d4b06a", // ochre
		FileTag:               "#c8a864", // parchment gold
		StickerTag:            "#8a6858", // terracotta
		ContactTag:            "#b08860", // warm caramel
		PollTag:               "#dcc070", // bright ochre
		LocationTag:           "#906858", // muted clay
		AnomalyTag:            "#a84840", // burnt sienna
		SentText:              "#eedcb0", // warm parchment
		ReceivedText:          "#dcc8a0", // slightly darker parchment
		SentName:              "#c8a864", // parchment gold — "me"
		ReceivedName:          "#a87c50", // aged leather — "them"
		QuotedSentText:        "#9a8460", // muted gold
		QuotedReceivedText:    "#887060", // muted sepia
		BadgeInk:              "#14110c",
		ButtonInk:             "#14110c",
		TagInk:                "#14110c",
		Cursor:                "#c8a864",
		QRLight:               "#FFFFFF",
		QRDark:                "#14110c",
		ShortcutActive:        "#201a12",
		SidebarActiveBg:       "#2a2018", // dark warm sepia active
		SidebarActiveUnreadBg: "#251a10", // darker unread
		SidebarWhitelistActiveBg: "#c8a864", // brand gold
		SidebarBlacklistActiveBg: "#a84840", // red
		ReplyPreviewBg:        "#1c1610",
		MessageSelectedBg:     "#1a140e",
		MediaTokenBg:          "#a87c50",
		MediaTokenPulseBg:     "#c8a864",
		Background:            "#14110c", // dark warm sepia-black
	}

	// Linen — warm off-white bg, pastel muted palette, soft and modern
	Linen = Theme{
		Brand:                 "#7a9e9f", // muted teal-sage
		Accent:                "#7a95b8", // dusty cornflower
		Purple:                "#9e8fbd", // dusty lavender
		Amber:                 "#c4986a", // muted caramel
		Red:                   "#c47878", // dusty rose
		Muted:                 "#b0a89e", // warm gray
		Text:                  "#3a3530", // warm near-black
		ImageTag:              "#c47878", // dusty rose
		VideoTag:              "#7a95b8", // cornflower
		AudioTag:              "#c4986a", // caramel
		FileTag:               "#7aab8a", // sage green
		StickerTag:            "#9e8fbd", // lavender
		ContactTag:            "#6aadb2", // muted teal
		PollTag:               "#c4986a", // caramel
		LocationTag:           "#7a8fc4", // periwinkle
		AnomalyTag:            "#c47878", // dusty rose
		SentText:              "#2d4a6b", // dark slate blue — sent messages
		ReceivedText:          "#3a3530", // warm dark — received
		SentName:              "#7a9e9f", // brand teal — "me"
		ReceivedName:          "#9e8fbd", // dusty lavender — "them"
		QuotedSentText:        "#8a9aaa", // muted blue-gray
		QuotedReceivedText:    "#aaa49e", // muted warm gray
		BadgeInk:              "#f9f7f4",
		ButtonInk:             "#f9f7f4",
		TagInk:                "#f9f7f4",
		Cursor:                "#7a95b8", // dusty cornflower cursor
		QRLight:               "#f9f7f4",
		QRDark:                "#3a3530",
		ShortcutActive:        "#e4eaf2", // soft blue-gray
		SidebarActiveBg:       "#e4ecf4", // light blue tint
		SidebarActiveUnreadBg: "#f5ede0", // soft warm amber tint
		SidebarWhitelistActiveBg: "#7a9e9f", // brand teal
		SidebarBlacklistActiveBg: "#c47878", // red
		ReplyPreviewBg:        "#edf0f6",
		MessageSelectedBg:     "#e8edf5",
		MediaTokenBg:          "#7a95b8", // dusty cornflower
		MediaTokenPulseBg:     "#a8bdd4", // lighter cornflower
		Background:            "#f9f7f4", // warm linen off-white
	}

	// Halo — airy porcelain base, powder blue cards, fresh citrus green, and deep cobalt anchors
	Halo = Theme{
		Brand:                 "#95B1EE", // powder blue
		Accent:                "#364C84", // deep cobalt
		Purple:                "#7f90c7", // soft periwinkle
		Amber:                 "#E7F1A8", // citrus-lime accent
		Red:                   "#7d91c9", // muted blue-rose fallback danger
		Muted:                 "#bfc5d6", // pale stone blue-gray
		Text:                  "#364C84", // strong cobalt text
		ImageTag:              "#95B1EE", // powder blue
		VideoTag:              "#364C84", // deep cobalt
		AudioTag:              "#E7F1A8", // citrus-lime
		FileTag:               "#7f90c7", // soft periwinkle
		StickerTag:            "#95B1EE", // powder blue
		ContactTag:            "#a7bbef", // lighter blue
		PollTag:               "#E7F1A8", // citrus-lime
		LocationTag:           "#6d84bb", // mid cobalt
		AnomalyTag:            "#364C84", // deep cobalt
		SentText:              "#364C84", // dark cobalt
		ReceivedText:          "#4a5f92", // slightly lighter cobalt
		SentName:              "#95B1EE", // powder blue
		ReceivedName:          "#364C84", // deep cobalt
		QuotedSentText:        "#7f90c7", // muted periwinkle
		QuotedReceivedText:    "#6d7ea8", // subdued cobalt
		BadgeInk:              "#FFFDF5", // porcelain white
		ButtonInk:             "#FFFDF5", // porcelain white
		TagInk:                "#364C84", // deep cobalt on light tags
		Cursor:                "#364C84", // deep cobalt
		QRLight:               "#FFFDF5", // porcelain base
		QRDark:                "#364C84", // deep cobalt
		ShortcutActive:        "#eef2fb", // cool white-blue
		SidebarActiveBg:       "#e8eefc", // pale powder-blue wash
		SidebarActiveUnreadBg: "#f1f5da", // pale citrus wash
		SidebarWhitelistActiveBg: "#95B1EE", // brand blue
		SidebarBlacklistActiveBg: "#7d91c9", // red
		ReplyPreviewBg:        "#f3f6fe", // soft porcelain-blue
		MessageSelectedBg:     "#e6ecfb", // powder-blue selection
		MediaTokenBg:          "#95B1EE", // powder blue
		MediaTokenPulseBg:     "#E7F1A8", // citrus pulse
		Background:            "#FFFDF5", // porcelain white
	}

	// Cornflower — deep cobalt canvas, porcelain contrast, powder-blue surfaces, and citrus highlights
	Cornflower = Theme{
		Brand:                 "#95B1EE", // powder blue highlight
		Accent:                "#E7F1A8", // citrus-lime accent
		Purple:                "#c8d4f6", // frosted blue-lilac
		Amber:                 "#FFFDF5", // porcelain light
		Red:                   "#7e95cc", // softened alert blue-violet
		Muted:                 "#8ea2d7", // muted cornflower
		Text:                  "#FFFDF5", // porcelain text
		ImageTag:              "#95B1EE", // powder blue
		VideoTag:              "#E7F1A8", // citrus-lime
		AudioTag:              "#FFFDF5", // porcelain
		FileTag:               "#c8d4f6", // pale frosted blue
		StickerTag:            "#95B1EE", // powder blue
		ContactTag:            "#d7e1fb", // pale ice-blue
		PollTag:               "#E7F1A8", // citrus-lime
		LocationTag:           "#cbd7f8", // pale blue
		AnomalyTag:            "#FFFDF5", // porcelain alert
		SentText:              "#E7F1A8", // citrus-lime outgoing text
		ReceivedText:          "#FFFDF5", // porcelain incoming text
		SentName:              "#E7F1A8", // citrus marker
		ReceivedName:          "#95B1EE", // powder blue
		QuotedSentText:        "#d9e4b3", // subdued lime-white
		QuotedReceivedText:    "#d5def8", // subdued frosted blue
		BadgeInk:              "#364C84", // deep cobalt
		ButtonInk:             "#364C84", // deep cobalt
		TagInk:                "#364C84", // deep cobalt
		Cursor:                "#FFFDF5", // porcelain cursor
		QRLight:               "#FFFDF5", // porcelain
		QRDark:                "#364C84", // cobalt
		ShortcutActive:        "#536aa3", // lighter cobalt chip
		SidebarActiveBg:       "#4b629d", // raised cobalt active
		SidebarActiveUnreadBg: "#5b74b0", // brighter unread blue
		SidebarWhitelistActiveBg: "#95B1EE", // brand blue
		SidebarBlacklistActiveBg: "#7e95cc", // red
		ReplyPreviewBg:        "#4a6099", // cobalt panel
		MessageSelectedBg:     "#556ca7", // selected cobalt
		MediaTokenBg:          "#95B1EE", // powder blue token
		MediaTokenPulseBg:     "#E7F1A8", // citrus pulse
		Background:            "#364C84", // source palette dark blue
	}

	// WhatsApp — classic WhatsApp greens on a dark app shell with the familiar outgoing bubble tint
	WhatsApp = Theme{
		Brand:                 "#25D366", // WhatsApp green
		Accent:                "#128C7E", // classic WhatsApp dark teal
		Purple:                "#34B7F1", // WhatsApp blue
		Amber:                 "#F0B429", // warm amber for counts and alerts
		Red:                   "#E57373", // soft alert red that fits the palette
		Muted:                 "#8696A0", // WhatsApp-style muted gray
		Text:                  "#E9EDEF", // dark theme foreground
		ImageTag:              "#25D366", // WhatsApp green
		VideoTag:              "#128C7E", // dark teal
		AudioTag:              "#34B7F1", // WhatsApp blue
		FileTag:               "#7AE582", // lighter green
		StickerTag:            "#7D8DFF", // cool indigo accent
		ContactTag:            "#5AC8A8", // aqua-green
		PollTag:               "#F0B429", // amber
		LocationTag:           "#34B7F1", // blue
		AnomalyTag:            "#25D366", // keep header chip on-brand
		SentText:              "#DCF8C6", // iconic sent bubble tint
		ReceivedText:          "#E9EDEF", // dark theme received text
		SentName:              "#25D366", // "me" label
		ReceivedName:          "#34B7F1", // "them" label
		QuotedSentText:        "#9FC49A", // dimmed sent bubble tint
		QuotedReceivedText:    "#AEB8BF", // dimmed foreground tint
		BadgeInk:              "#0B141A",
		ButtonInk:             "#0B141A",
		TagInk:                "#0B141A",
		Cursor:                "#25D366", // WhatsApp green cursor
		QRLight:               "#FFFFFF",
		QRDark:                "#0B141A",
		ShortcutActive:        "#202C33", // dark app chrome
		SidebarActiveBg:       "#202C33", // WhatsApp dark list selection
		SidebarActiveUnreadBg: "#1F3A33", // green-tinted dark selection
		SidebarWhitelistActiveBg: "#25D366", // brand green
		SidebarBlacklistActiveBg: "#E57373", // red
		ReplyPreviewBg:        "#111B21", // dark app panel
		MessageSelectedBg:     "#1A2A2F", // slightly lifted dark selection
		MediaTokenBg:          "#128C7E", // WhatsApp dark teal
		MediaTokenPulseBg:     "#25D366", // pulse with the brand green
		Background:            "#0B141A", // WhatsApp dark app background
	}

	// TokyoNight — deep navy #1a1b26 bg, cool blue-purple chrome, warm green/cyan accents
	TokyoNight = Theme{
		Brand:                 "#9ece6a", // official Tokyo Night green
		Accent:                "#7dcfff", // official Tokyo Night cyan
		Purple:                "#bb9af7", // official Tokyo Night purple
		Amber:                 "#e0af68", // official Tokyo Night orange-gold
		Red:                   "#f7768e", // official Tokyo Night red
		Muted:                 "#565f89", // blue-gray comment color
		Text:                  "#c0caf5", // blue-white foreground
		ImageTag:              "#f7768e", // red
		VideoTag:              "#7dcfff", // cyan
		AudioTag:              "#fbbf24", // warm yellow — distinct from amber
		FileTag:               "#9ece6a", // green
		StickerTag:            "#bb9af7", // purple
		ContactTag:            "#73daca", // teal
		PollTag:               "#e0af68", // amber-gold
		LocationTag:           "#7aa2f7", // blue
		AnomalyTag:            "#f7768e", // red
		SentText:              "#c3e88d", // soft lime-green — ties sent to brand
		ReceivedText:          "#bab0f0", // soft periwinkle — incoming, purple family
		SentName:              "#9ece6a", // brand green — "me" in groups
		ReceivedName:          "#bb9af7", // vivid purple — "them", bold hierarchy
		QuotedSentText:        "#8ca38b", // 50% blend sentText+muted — ghost green for quoted own
		QuotedReceivedText:    "#8887bc", // 50% blend receivedText+muted — ghost purple for quoted theirs
		BadgeInk:              "#1a1b26",
		ButtonInk:             "#1a1b26",
		TagInk:                "#1a1b26",
		Cursor:                "#c0caf5", // text-color cursor — less harsh than white
		QRLight:               "#FFFFFF",
		QRDark:                "#000000",
		ShortcutActive:        "#24283b", // Tokyo Night surface bg
		SidebarActiveBg:       "#2e3566", // rich navy-blue tint — cool, on-theme
		SidebarActiveUnreadBg: "#252a40", // cool navy for unread — no amber bleed
		SidebarWhitelistActiveBg: "#9ece6a", // brand green
		SidebarBlacklistActiveBg: "#f7768e", // red
		ReplyPreviewBg:        "#1d2545", // deep navy reply context
		MessageSelectedBg:     "#232845", // subtle navy highlight
		MediaTokenBg:          "#f7768e", // theme's own red — more cohesive
		MediaTokenPulseBg:     "#ff9eb5", // soft red-pink pulse
		Background:            "#1a1b26", // Tokyo Night canonical bg
	}

	// Catppuccin Mocha — official Catppuccin Mocha palette, #1e1e2e bg
	Catppuccin = Theme{
		Brand:                 "#a6e3a1", // Mocha Green (official)
		Accent:                "#89b4fa", // Mocha Blue (official)
		Purple:                "#cba4f7", // Mocha Mauve (official)
		Amber:                 "#f9e2af", // Mocha Yellow (official)
		Red:                   "#f38ba8", // Mocha Red (official)
		Muted:                 "#6c7086", // Mocha Overlay0 (official)
		Text:                  "#cdd6f4", // Mocha Text (official)
		ImageTag:              "#f38ba8", // Mocha Red
		VideoTag:              "#89b4fa", // Mocha Blue
		AudioTag:              "#fab387", // Mocha Peach
		FileTag:               "#a6e3a1", // Mocha Green
		StickerTag:            "#cba4f7", // Mocha Mauve
		ContactTag:            "#94e2d5", // Mocha Teal
		PollTag:               "#f9e2af", // Mocha Yellow
		LocationTag:           "#89dceb", // Mocha Sky
		AnomalyTag:            "#f38ba8", // Mocha Red
		SentText:              "#d0efc5", // soft sage-mint — brand green family
		ReceivedText:          "#dcd0f5", // soft mauve-lavender — incoming, purple family
		SentName:              "#a6e3a1", // brand green — "me" label
		ReceivedName:          "#cba4f7", // vivid mauve — "them" label
		QuotedSentText:        "#9eafa5", // 50% blend sentText+muted — ghost sage for quoted own
		QuotedReceivedText:    "#a4a0bd", // 50% blend receivedText+muted — ghost mauve for quoted theirs
		BadgeInk:              "#1e1e2e",
		ButtonInk:             "#1e1e2e",
		TagInk:                "#1e1e2e",
		Cursor:                "#cdd6f4", // text-color cursor
		QRLight:               "#FFFFFF",
		QRDark:                "#000000",
		ShortcutActive:        "#2a2a3d", // Mocha Surface0 variant
		SidebarActiveBg:       "#313244", // Mocha Surface0 (official)
		SidebarActiveUnreadBg: "#2a2545", // mauve-tinted navy — on-theme for unread
		SidebarWhitelistActiveBg: "#a6e3a1", // brand green
		SidebarBlacklistActiveBg: "#f38ba8", // red
		ReplyPreviewBg:        "#272040", // dark mauve context
		MessageSelectedBg:     "#252040", // deep mauve selection
		MediaTokenBg:          "#f38ba8", // Mocha Red
		MediaTokenPulseBg:     "#f5bde6", // Mocha Pink — soft pulse
		Background:            "#1e1e2e",
	}

	// Monokai — classic #272822 bg, vivid 6-color palette
	Monokai = Theme{
		Brand:                 "#a6e22e", // Monokai green
		Accent:                "#66d9ef", // Monokai cyan
		Purple:                "#ae81ff", // Monokai purple
		Amber:                 "#e6db74", // Monokai yellow
		Red:                   "#f92672", // Monokai red
		Muted:                 "#75715e", // Monokai comment
		Text:                  "#f8f8f2", // Monokai foreground
		ImageTag:              "#f92672", // red
		VideoTag:              "#66d9ef", // cyan
		AudioTag:              "#fd971f", // Monokai orange — distinct from yellow PollTag
		FileTag:               "#a6e22e", // green
		StickerTag:            "#ae81ff", // purple
		ContactTag:            "#78dce8", // Monokai Pro teal — distinct from cyan
		PollTag:               "#e6db74", // yellow
		LocationTag:           "#66d9ef", // cyan (exact official)
		AnomalyTag:            "#f92672", // red — using official red, not external pink
		SentText:              "#d9f7a7", // warm lime — outgoing, brand green family
		ReceivedText:          "#c0eeff", // soft cyan-white — incoming, accent family
		SentName:              "#a6e22e", // brand green — "me" label
		ReceivedName:          "#ae81ff", // purple — "them", clearly distinct
		QuotedSentText:        "#a7b482", // 50% blend sentText+muted — ghost olive for quoted own
		QuotedReceivedText:    "#9aafae", // 50% blend receivedText+muted — ghost cyan for quoted theirs
		BadgeInk:              "#1f1f1f",
		ButtonInk:             "#1f1f1f",
		TagInk:                "#1f1f1f",
		Cursor:                "#f8f8f2", // foreground color cursor
		QRLight:               "#FFFFFF",
		QRDark:                "#000000",
		ShortcutActive:        "#2a2820", // darker than Monokai bg
		SidebarActiveBg:       "#3e3d32", // Monokai bg lightened
		SidebarActiveUnreadBg: "#3e3520", // warm yellow-green tint — on-theme
		SidebarWhitelistActiveBg: "#a6e22e", // brand green
		SidebarBlacklistActiveBg: "#f92672", // red
		ReplyPreviewBg:        "#332c18", // dark warm Monokai
		MessageSelectedBg:     "#2d2910", // very dark warm highlight
		MediaTokenBg:          "#f92672", // Monokai red
		MediaTokenPulseBg:     "#ff6b9d", // soft pink pulse
		Background:            "#272822",
	}

	// Charcoal — pure monochrome, dark #1c1c1c bg, deliberate luminance steps
	Charcoal = Theme{
		Brand:                 "#ebebeb", // near-white brand
		Accent:                "#d0d0d0", // lighter gray accent
		Purple:                "#b8b8b8", // mid-gray "purple"
		Amber:                 "#e0e0e0", // lighter gray "amber"
		Red:                   "#a0a0a0", // darker gray "red"
		Muted:                 "#686868", // clearly muted
		Text:                  "#f0f0f0", // off-white text — easier on eyes
		ImageTag:              "#e2e2e2", // brightest tag tier
		VideoTag:              "#cccccc", // second tier
		AudioTag:              "#b8b8b8", // third tier
		FileTag:               "#d8d8d8", // bright-medium
		StickerTag:            "#a8a8a8", // lower mid
		ContactTag:            "#c0c0c0", // medium
		PollTag:               "#d0d0d0", // upper mid
		LocationTag:           "#989898", // darker tier
		AnomalyTag:            "#808080", // darkest — visually distinct as anomaly
		SentText:              "#f0f0f0", // near-white — my messages pop
		ReceivedText:          "#bebebe", // clearly dimmer — their messages
		SentName:              "#ebebeb", // brightest gray for "me"
		ReceivedName:          "#adadad", // distinctly subordinate
		QuotedSentText:        "#acacac", // 50% blend sentText+muted — dimmer gray for quoted own
		QuotedReceivedText:    "#939393", // 50% blend receivedText+muted — darker gray for quoted theirs
		BadgeInk:              "#1a1a1a",
		ButtonInk:             "#1a1a1a",
		TagInk:                "#1a1a1a",
		Cursor:                "#f0f0f0", // text-color cursor
		QRLight:               "#FFFFFF",
		QRDark:                "#000000",
		ShortcutActive:        "#2e2e2e",
		SidebarActiveBg:       "#2d2d2d",
		SidebarActiveUnreadBg: "#333333", // neutral gray, no warm tint
		SidebarWhitelistActiveBg: "#ebebeb", // brand gray
		SidebarBlacklistActiveBg: "#a0a0a0", // red
		ReplyPreviewBg:        "#3f3f3f",
		MessageSelectedBg:     "#383838",
		MediaTokenBg:          "#c8c8c8",
		MediaTokenPulseBg:     "#e0e0e0",
		Background:            "#1c1c1c",
	}

	// Aurora — Northern Lights on deep night sky, vivid electric palette across the spectrum
	Aurora = Theme{
		Brand:                 "#50fa7b", // electric green — aurora's primary band
		Accent:                "#8be9fd", // electric cyan — aurora blue
		Purple:                "#bd93f9", // aurora violet/amethyst
		Amber:                 "#ffb86c", // aurora warm orange glow
		Red:                   "#ff5555", // aurora red fringe
		Muted:                 "#44475a", // deep night sky muted
		Text:                  "#f8f8f2", // star-white foreground
		ImageTag:              "#ff79c6", // aurora pink/magenta
		VideoTag:              "#8be9fd", // electric cyan
		AudioTag:              "#f1fa8c", // aurora yellow-green flash
		FileTag:               "#50fa7b", // electric green
		StickerTag:            "#bd93f9", // violet
		ContactTag:            "#6dcfb4", // teal — distinct from cyan and green
		PollTag:               "#ffb86c", // warm orange
		LocationTag:           "#80bfff", // sky blue
		AnomalyTag:            "#ff5555", // aurora red
		SentText:              "#ccffdc", // soft mint — outgoing, green family
		ReceivedText:          "#e0d8ff", // soft violet — incoming, DISTINCT hue from sent
		SentName:              "#50fa7b", // brand electric green — "me"
		ReceivedName:          "#bd93f9", // violet — "them", clearly distinct from green
		QuotedSentText:        "#88a39b", // 50% blend sentText+muted — ghost mint for quoted own
		QuotedReceivedText:    "#928fac", // 50% blend receivedText+muted — ghost violet for quoted theirs
		BadgeInk:              "#0d1117",
		ButtonInk:             "#0d1117",
		TagInk:                "#0d1117",
		Cursor:                "#50fa7b", // brand green cursor — glowing
		QRLight:               "#FFFFFF",
		QRDark:                "#0d1117",
		ShortcutActive:        "#1a1e2e",
		SidebarActiveBg:       "#1e2540", // deep aurora night sky blue
		SidebarActiveUnreadBg: "#18272a", // dark teal — aurora green band
		SidebarWhitelistActiveBg: "#50fa7b", // brand green
		SidebarBlacklistActiveBg: "#ff5555", // red
		ReplyPreviewBg:        "#1a2030",
		MessageSelectedBg:     "#14192a", // very dark navy
		MediaTokenBg:          "#ff79c6", // aurora pink
		MediaTokenPulseBg:     "#ffb3e0", // soft pink pulse
		Background:            "#0d1117",
	}

	// Sakura — Cherry blossom, dark plum bg, pink/rose/lavender palette
	Sakura = Theme{
		Brand:                 "#ffb7d5", // sakura petal — soft light pink
		Accent:                "#f472b6", // vibrant cherry — deeper pink
		Purple:                "#e879f9", // wisteria/orchid
		Amber:                 "#fcd34d", // golden stamen — warm contrast
		Red:                   "#fb7185", // coral rose
		Muted:                 "#8b6070", // dusty rose-gray
		Text:                  "#fce8f3", // warm blush white
		ImageTag:              "#f472b6", // cherry pink
		VideoTag:              "#c084fc", // soft purple — cool contrast to pink
		AudioTag:              "#fcd34d", // golden stamen
		FileTag:               "#a5f3fc", // sakura sky blue — cherry blossom viewing sky
		StickerTag:            "#e879f9", // orchid/wisteria
		ContactTag:            "#fda4af", // soft rose
		PollTag:               "#fde68a", // soft gold
		LocationTag:           "#93c5fd", // spring sky blue
		AnomalyTag:            "#fb7185", // coral
		SentText:              "#ffe8f5", // warm rose-white — soft, "mine"
		ReceivedText:          "#f0e0ff", // soft lavender — "theirs", distinct hue
		SentName:              "#ffb7d5", // brand light-pink — "me" label
		ReceivedName:          "#e879f9", // vivid orchid — "them", pops against pink
		QuotedSentText:        "#c5a4b2", // 50% blend sentText+muted — ghost rose for quoted own
		QuotedReceivedText:    "#bda0b7", // 50% blend receivedText+muted — ghost lavender for quoted theirs
		BadgeInk:              "#1a0d14",
		ButtonInk:             "#1a0d14",
		TagInk:                "#1a0d14",
		Cursor:                "#ffb7d5", // brand pink cursor — thematic
		QRLight:               "#FFFFFF",
		QRDark:                "#1a0d14",
		ShortcutActive:        "#231019",
		SidebarActiveBg:       "#3d1f35", // rich plum — active
		SidebarActiveUnreadBg: "#2a1020", // clearly darker plum — unread, distinct from active
		SidebarWhitelistActiveBg: "#ffb7d5", // brand pink
		SidebarBlacklistActiveBg: "#fb7185", // red
		ReplyPreviewBg:        "#4a1d3f", // deep plum reply context
		MessageSelectedBg:     "#351430", // deep burgundy selection
		MediaTokenBg:          "#db2777",
		MediaTokenPulseBg:     "#f472b6",
		Background:            "#1a0d14",
	}

	// Abyssal — deep ocean bioluminescence, near-black #020810 bg
	Abyssal = Theme{
		Brand:                 "#00e5c8", // bioluminescent teal — signature glow
		Accent:                "#00cfff", // electric cyan
		Purple:                "#7b6cff", // deep ocean violet
		Amber:                 "#ffb347", // anglerfish amber lure
		Red:                   "#ff4f6e", // bioluminescent red
		Muted:                 "#2d4a5a", // deep ocean blue-gray
		Text:                  "#c4dce8", // cool ocean-light text
		ImageTag:              "#ff79b0", // pink bioluminescence — vivid, unique
		VideoTag:              "#00cfff", // electric cyan
		AudioTag:              "#ffb347", // anglerfish amber
		FileTag:               "#00e5c8", // teal bioluminescence
		StickerTag:            "#7b6cff", // deep violet
		ContactTag:            "#40e0d0", // turquoise — distinct from teal and cyan above
		PollTag:               "#ffd166", // golden yellow — distinct from amber
		LocationTag:           "#74b9ff", // deep ocean blue — distinct from cyan
		AnomalyTag:            "#ff4f6e", // bioluminescent red
		SentText:              "#a8f0e8", // bioluminescent teal-tinted — outgoing
		ReceivedText:          "#b8d0ff", // soft electric blue — incoming, distinct hue
		SentName:              "#00e5c8", // brand teal — "me", glowing
		ReceivedName:          "#7b6cff", // deep violet — "them", clearly distinct
		QuotedSentText:        "#6a9da1", // 50% blend sentText+muted — ghost teal for quoted own
		QuotedReceivedText:    "#728dac", // 50% blend receivedText+muted — ghost blue for quoted theirs
		BadgeInk:              "#020c14",
		ButtonInk:             "#020c14",
		TagInk:                "#020c14",
		Cursor:                "#00e5c8", // glowing teal cursor
		QRLight:               "#FFFFFF",
		QRDark:                "#020c14",
		ShortcutActive:        "#0d1f2d",
		SidebarActiveBg:       "#0a2233",
		SidebarActiveUnreadBg: "#102a20", // dark teal-green — distinct from blue active
		SidebarWhitelistActiveBg: "#00e5c8", // brand teal
		SidebarBlacklistActiveBg: "#ff4f6e", // red
		ReplyPreviewBg:        "#0c2030",
		MessageSelectedBg:     "#091c28",
		MediaTokenBg:          "#0077aa",
		MediaTokenPulseBg:     "#00cfff",
		Background:            "#020810",
	}

	Cyberpunk = Theme{
		Brand:                 "#ff007f",
		Accent:                "#00f0ff",
		Purple:                "#ab00ff",
		Amber:                 "#fff000",
		Red:                   "#ff003c",
		Muted:                 "#1a1a2e",
		Text:                  "#d0c0ff",
		ImageTag:              "#ff007f",
		VideoTag:              "#00f0ff",
		AudioTag:              "#fff000",
		FileTag:               "#00ff66",
		StickerTag:            "#ab00ff",
		ContactTag:            "#ff007f",
		PollTag:               "#fff000",
		LocationTag:           "#00f0ff",
		AnomalyTag:            "#ff003c",
		SentText:              "#ff5599",
		ReceivedText:          "#00e0ff",
		SentName:              "#ff007f",
		ReceivedName:          "#ab00ff",
		QuotedSentText:        "#994477",
		QuotedReceivedText:    "#448899",
		BadgeInk:              "#05050a",
		ButtonInk:             "#05050a",
		TagInk:                "#05050a",
		Cursor:                "#ff007f",
		QRLight:               "#FFFFFF",
		QRDark:                "#05050a",
		ShortcutActive:        "#1a0e26",
		SidebarActiveBg:       "#1f0033",
		SidebarActiveUnreadBg: "#292900",
		SidebarWhitelistActiveBg: "#ff007f", // brand magenta
		SidebarBlacklistActiveBg: "#ff003c", // red
		ReplyPreviewBg:        "#12061c",
		MessageSelectedBg:     "#1c092c",
		MediaTokenBg:          "#ff007f",
		MediaTokenPulseBg:     "#00f0ff",
		Background:            "#0a0a14",
	}

	Lilac = Theme{
		Brand:                 "#8e44ad",
		Accent:                "#b388ff",
		Purple:                "#8e44ad",
		Amber:                 "#e67e22",
		Red:                   "#c0392b",
		Muted:                 "#9a8b9e",
		Text:                  "#2a1b35",
		ImageTag:              "#8e44ad",
		VideoTag:              "#7b1fa2",
		AudioTag:              "#d81b60",
		FileTag:               "#3f51b5",
		StickerTag:            "#b388ff",
		ContactTag:            "#00acc1",
		PollTag:               "#fb8c00",
		LocationTag:           "#5c6bc0",
		AnomalyTag:            "#c0392b",
		SentText:              "#1a0f30",
		ReceivedText:          "#2a1b35",
		SentName:              "#8e44ad",
		ReceivedName:          "#512da8",
		QuotedSentText:        "#7a6b8e",
		QuotedReceivedText:    "#8a7b9e",
		BadgeInk:              "#fbf8ff",
		ButtonInk:             "#fbf8ff",
		TagInk:                "#fbf8ff",
		Cursor:                "#8e44ad",
		QRLight:               "#fbf8ff",
		QRDark:                "#2a1b35",
		ShortcutActive:        "#f3e5f5",
		SidebarActiveBg:       "#f3e5f5",
		SidebarActiveUnreadBg: "#ede7f6",
		SidebarWhitelistActiveBg: "#8e44ad", // brand purple
		SidebarBlacklistActiveBg: "#c0392b", // red
		ReplyPreviewBg:        "#f7f0fc",
		MessageSelectedBg:     "#f0e5fa",
		MediaTokenBg:          "#b388ff",
		MediaTokenPulseBg:     "#d1c4e9",
		Background:            "#fbf8ff",
	}
)
