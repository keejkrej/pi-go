// Ported from chalk@6.0.0 source/index.js (one getter per ansi-styles entry). Generated from the ansi-styles table.

package ansi

// Reset starts a chain with the reset modifier style.
func (c *Chalk) Reset() *Builder { return c.chainStyle("\x1b[0m", "\x1b[0m") }

// Reset extends the chain with the reset modifier style.
func (b *Builder) Reset() *Builder { return b.chainStyle("\x1b[0m", "\x1b[0m") }

// Reset styles text with the default instance (`chalk.reset(text)`).
func Reset(text string) string { return Default().Reset().Apply(text) }

// Bold starts a chain with the bold modifier style.
func (c *Chalk) Bold() *Builder { return c.chainStyle("\x1b[1m", "\x1b[22m") }

// Bold extends the chain with the bold modifier style.
func (b *Builder) Bold() *Builder { return b.chainStyle("\x1b[1m", "\x1b[22m") }

// Bold styles text with the default instance (`chalk.bold(text)`).
func Bold(text string) string { return Default().Bold().Apply(text) }

// Dim starts a chain with the dim modifier style.
func (c *Chalk) Dim() *Builder { return c.chainStyle("\x1b[2m", "\x1b[22m") }

// Dim extends the chain with the dim modifier style.
func (b *Builder) Dim() *Builder { return b.chainStyle("\x1b[2m", "\x1b[22m") }

// Dim styles text with the default instance (`chalk.dim(text)`).
func Dim(text string) string { return Default().Dim().Apply(text) }

// Italic starts a chain with the italic modifier style.
func (c *Chalk) Italic() *Builder { return c.chainStyle("\x1b[3m", "\x1b[23m") }

// Italic extends the chain with the italic modifier style.
func (b *Builder) Italic() *Builder { return b.chainStyle("\x1b[3m", "\x1b[23m") }

// Italic styles text with the default instance (`chalk.italic(text)`).
func Italic(text string) string { return Default().Italic().Apply(text) }

// Underline starts a chain with the underline modifier style.
func (c *Chalk) Underline() *Builder { return c.chainStyle("\x1b[4m", "\x1b[24m") }

// Underline extends the chain with the underline modifier style.
func (b *Builder) Underline() *Builder { return b.chainStyle("\x1b[4m", "\x1b[24m") }

// Underline styles text with the default instance (`chalk.underline(text)`).
func Underline(text string) string { return Default().Underline().Apply(text) }

// UnderlineDouble starts a chain with the underlineDouble modifier style.
func (c *Chalk) UnderlineDouble() *Builder { return c.chainStyle("\x1b[4:2m", "\x1b[24m") }

// UnderlineDouble extends the chain with the underlineDouble modifier style.
func (b *Builder) UnderlineDouble() *Builder { return b.chainStyle("\x1b[4:2m", "\x1b[24m") }

// UnderlineDouble styles text with the default instance (`chalk.underlineDouble(text)`).
func UnderlineDouble(text string) string { return Default().UnderlineDouble().Apply(text) }

// UnderlineCurly starts a chain with the underlineCurly modifier style.
func (c *Chalk) UnderlineCurly() *Builder { return c.chainStyle("\x1b[4:3m", "\x1b[24m") }

// UnderlineCurly extends the chain with the underlineCurly modifier style.
func (b *Builder) UnderlineCurly() *Builder { return b.chainStyle("\x1b[4:3m", "\x1b[24m") }

// UnderlineCurly styles text with the default instance (`chalk.underlineCurly(text)`).
func UnderlineCurly(text string) string { return Default().UnderlineCurly().Apply(text) }

// UnderlineDotted starts a chain with the underlineDotted modifier style.
func (c *Chalk) UnderlineDotted() *Builder { return c.chainStyle("\x1b[4:4m", "\x1b[24m") }

// UnderlineDotted extends the chain with the underlineDotted modifier style.
func (b *Builder) UnderlineDotted() *Builder { return b.chainStyle("\x1b[4:4m", "\x1b[24m") }

// UnderlineDotted styles text with the default instance (`chalk.underlineDotted(text)`).
func UnderlineDotted(text string) string { return Default().UnderlineDotted().Apply(text) }

// UnderlineDashed starts a chain with the underlineDashed modifier style.
func (c *Chalk) UnderlineDashed() *Builder { return c.chainStyle("\x1b[4:5m", "\x1b[24m") }

// UnderlineDashed extends the chain with the underlineDashed modifier style.
func (b *Builder) UnderlineDashed() *Builder { return b.chainStyle("\x1b[4:5m", "\x1b[24m") }

// UnderlineDashed styles text with the default instance (`chalk.underlineDashed(text)`).
func UnderlineDashed(text string) string { return Default().UnderlineDashed().Apply(text) }

// Overline starts a chain with the overline modifier style.
func (c *Chalk) Overline() *Builder { return c.chainStyle("\x1b[53m", "\x1b[55m") }

// Overline extends the chain with the overline modifier style.
func (b *Builder) Overline() *Builder { return b.chainStyle("\x1b[53m", "\x1b[55m") }

// Overline styles text with the default instance (`chalk.overline(text)`).
func Overline(text string) string { return Default().Overline().Apply(text) }

// Inverse starts a chain with the inverse modifier style.
func (c *Chalk) Inverse() *Builder { return c.chainStyle("\x1b[7m", "\x1b[27m") }

// Inverse extends the chain with the inverse modifier style.
func (b *Builder) Inverse() *Builder { return b.chainStyle("\x1b[7m", "\x1b[27m") }

// Inverse styles text with the default instance (`chalk.inverse(text)`).
func Inverse(text string) string { return Default().Inverse().Apply(text) }

// Hidden starts a chain with the hidden modifier style.
func (c *Chalk) Hidden() *Builder { return c.chainStyle("\x1b[8m", "\x1b[28m") }

// Hidden extends the chain with the hidden modifier style.
func (b *Builder) Hidden() *Builder { return b.chainStyle("\x1b[8m", "\x1b[28m") }

// Hidden styles text with the default instance (`chalk.hidden(text)`).
func Hidden(text string) string { return Default().Hidden().Apply(text) }

// Strikethrough starts a chain with the strikethrough modifier style.
func (c *Chalk) Strikethrough() *Builder { return c.chainStyle("\x1b[9m", "\x1b[29m") }

// Strikethrough extends the chain with the strikethrough modifier style.
func (b *Builder) Strikethrough() *Builder { return b.chainStyle("\x1b[9m", "\x1b[29m") }

// Strikethrough styles text with the default instance (`chalk.strikethrough(text)`).
func Strikethrough(text string) string { return Default().Strikethrough().Apply(text) }

// Black starts a chain with the black foreground color style.
func (c *Chalk) Black() *Builder { return c.chainStyle("\x1b[30m", "\x1b[39m") }

// Black extends the chain with the black foreground color style.
func (b *Builder) Black() *Builder { return b.chainStyle("\x1b[30m", "\x1b[39m") }

// Black styles text with the default instance (`chalk.black(text)`).
func Black(text string) string { return Default().Black().Apply(text) }

// Red starts a chain with the red foreground color style.
func (c *Chalk) Red() *Builder { return c.chainStyle("\x1b[31m", "\x1b[39m") }

// Red extends the chain with the red foreground color style.
func (b *Builder) Red() *Builder { return b.chainStyle("\x1b[31m", "\x1b[39m") }

// Red styles text with the default instance (`chalk.red(text)`).
func Red(text string) string { return Default().Red().Apply(text) }

// Green starts a chain with the green foreground color style.
func (c *Chalk) Green() *Builder { return c.chainStyle("\x1b[32m", "\x1b[39m") }

// Green extends the chain with the green foreground color style.
func (b *Builder) Green() *Builder { return b.chainStyle("\x1b[32m", "\x1b[39m") }

// Green styles text with the default instance (`chalk.green(text)`).
func Green(text string) string { return Default().Green().Apply(text) }

// Yellow starts a chain with the yellow foreground color style.
func (c *Chalk) Yellow() *Builder { return c.chainStyle("\x1b[33m", "\x1b[39m") }

// Yellow extends the chain with the yellow foreground color style.
func (b *Builder) Yellow() *Builder { return b.chainStyle("\x1b[33m", "\x1b[39m") }

// Yellow styles text with the default instance (`chalk.yellow(text)`).
func Yellow(text string) string { return Default().Yellow().Apply(text) }

// Blue starts a chain with the blue foreground color style.
func (c *Chalk) Blue() *Builder { return c.chainStyle("\x1b[34m", "\x1b[39m") }

// Blue extends the chain with the blue foreground color style.
func (b *Builder) Blue() *Builder { return b.chainStyle("\x1b[34m", "\x1b[39m") }

// Blue styles text with the default instance (`chalk.blue(text)`).
func Blue(text string) string { return Default().Blue().Apply(text) }

// Magenta starts a chain with the magenta foreground color style.
func (c *Chalk) Magenta() *Builder { return c.chainStyle("\x1b[35m", "\x1b[39m") }

// Magenta extends the chain with the magenta foreground color style.
func (b *Builder) Magenta() *Builder { return b.chainStyle("\x1b[35m", "\x1b[39m") }

// Magenta styles text with the default instance (`chalk.magenta(text)`).
func Magenta(text string) string { return Default().Magenta().Apply(text) }

// Cyan starts a chain with the cyan foreground color style.
func (c *Chalk) Cyan() *Builder { return c.chainStyle("\x1b[36m", "\x1b[39m") }

// Cyan extends the chain with the cyan foreground color style.
func (b *Builder) Cyan() *Builder { return b.chainStyle("\x1b[36m", "\x1b[39m") }

// Cyan styles text with the default instance (`chalk.cyan(text)`).
func Cyan(text string) string { return Default().Cyan().Apply(text) }

// White starts a chain with the white foreground color style.
func (c *Chalk) White() *Builder { return c.chainStyle("\x1b[37m", "\x1b[39m") }

// White extends the chain with the white foreground color style.
func (b *Builder) White() *Builder { return b.chainStyle("\x1b[37m", "\x1b[39m") }

// White styles text with the default instance (`chalk.white(text)`).
func White(text string) string { return Default().White().Apply(text) }

// BlackBright starts a chain with the blackBright foreground color style.
func (c *Chalk) BlackBright() *Builder { return c.chainStyle("\x1b[90m", "\x1b[39m") }

// BlackBright extends the chain with the blackBright foreground color style.
func (b *Builder) BlackBright() *Builder { return b.chainStyle("\x1b[90m", "\x1b[39m") }

// BlackBright styles text with the default instance (`chalk.blackBright(text)`).
func BlackBright(text string) string { return Default().BlackBright().Apply(text) }

// Gray starts a chain with the gray foreground color style.
func (c *Chalk) Gray() *Builder { return c.chainStyle("\x1b[90m", "\x1b[39m") }

// Gray extends the chain with the gray foreground color style.
func (b *Builder) Gray() *Builder { return b.chainStyle("\x1b[90m", "\x1b[39m") }

// Gray styles text with the default instance (`chalk.gray(text)`).
func Gray(text string) string { return Default().Gray().Apply(text) }

// Grey starts a chain with the grey foreground color style.
func (c *Chalk) Grey() *Builder { return c.chainStyle("\x1b[90m", "\x1b[39m") }

// Grey extends the chain with the grey foreground color style.
func (b *Builder) Grey() *Builder { return b.chainStyle("\x1b[90m", "\x1b[39m") }

// Grey styles text with the default instance (`chalk.grey(text)`).
func Grey(text string) string { return Default().Grey().Apply(text) }

// RedBright starts a chain with the redBright foreground color style.
func (c *Chalk) RedBright() *Builder { return c.chainStyle("\x1b[91m", "\x1b[39m") }

// RedBright extends the chain with the redBright foreground color style.
func (b *Builder) RedBright() *Builder { return b.chainStyle("\x1b[91m", "\x1b[39m") }

// RedBright styles text with the default instance (`chalk.redBright(text)`).
func RedBright(text string) string { return Default().RedBright().Apply(text) }

// GreenBright starts a chain with the greenBright foreground color style.
func (c *Chalk) GreenBright() *Builder { return c.chainStyle("\x1b[92m", "\x1b[39m") }

// GreenBright extends the chain with the greenBright foreground color style.
func (b *Builder) GreenBright() *Builder { return b.chainStyle("\x1b[92m", "\x1b[39m") }

// GreenBright styles text with the default instance (`chalk.greenBright(text)`).
func GreenBright(text string) string { return Default().GreenBright().Apply(text) }

// YellowBright starts a chain with the yellowBright foreground color style.
func (c *Chalk) YellowBright() *Builder { return c.chainStyle("\x1b[93m", "\x1b[39m") }

// YellowBright extends the chain with the yellowBright foreground color style.
func (b *Builder) YellowBright() *Builder { return b.chainStyle("\x1b[93m", "\x1b[39m") }

// YellowBright styles text with the default instance (`chalk.yellowBright(text)`).
func YellowBright(text string) string { return Default().YellowBright().Apply(text) }

// BlueBright starts a chain with the blueBright foreground color style.
func (c *Chalk) BlueBright() *Builder { return c.chainStyle("\x1b[94m", "\x1b[39m") }

// BlueBright extends the chain with the blueBright foreground color style.
func (b *Builder) BlueBright() *Builder { return b.chainStyle("\x1b[94m", "\x1b[39m") }

// BlueBright styles text with the default instance (`chalk.blueBright(text)`).
func BlueBright(text string) string { return Default().BlueBright().Apply(text) }

// MagentaBright starts a chain with the magentaBright foreground color style.
func (c *Chalk) MagentaBright() *Builder { return c.chainStyle("\x1b[95m", "\x1b[39m") }

// MagentaBright extends the chain with the magentaBright foreground color style.
func (b *Builder) MagentaBright() *Builder { return b.chainStyle("\x1b[95m", "\x1b[39m") }

// MagentaBright styles text with the default instance (`chalk.magentaBright(text)`).
func MagentaBright(text string) string { return Default().MagentaBright().Apply(text) }

// CyanBright starts a chain with the cyanBright foreground color style.
func (c *Chalk) CyanBright() *Builder { return c.chainStyle("\x1b[96m", "\x1b[39m") }

// CyanBright extends the chain with the cyanBright foreground color style.
func (b *Builder) CyanBright() *Builder { return b.chainStyle("\x1b[96m", "\x1b[39m") }

// CyanBright styles text with the default instance (`chalk.cyanBright(text)`).
func CyanBright(text string) string { return Default().CyanBright().Apply(text) }

// WhiteBright starts a chain with the whiteBright foreground color style.
func (c *Chalk) WhiteBright() *Builder { return c.chainStyle("\x1b[97m", "\x1b[39m") }

// WhiteBright extends the chain with the whiteBright foreground color style.
func (b *Builder) WhiteBright() *Builder { return b.chainStyle("\x1b[97m", "\x1b[39m") }

// WhiteBright styles text with the default instance (`chalk.whiteBright(text)`).
func WhiteBright(text string) string { return Default().WhiteBright().Apply(text) }

// BgBlack starts a chain with the bgBlack background color style.
func (c *Chalk) BgBlack() *Builder { return c.chainStyle("\x1b[40m", "\x1b[49m") }

// BgBlack extends the chain with the bgBlack background color style.
func (b *Builder) BgBlack() *Builder { return b.chainStyle("\x1b[40m", "\x1b[49m") }

// BgBlack styles text with the default instance (`chalk.bgBlack(text)`).
func BgBlack(text string) string { return Default().BgBlack().Apply(text) }

// BgRed starts a chain with the bgRed background color style.
func (c *Chalk) BgRed() *Builder { return c.chainStyle("\x1b[41m", "\x1b[49m") }

// BgRed extends the chain with the bgRed background color style.
func (b *Builder) BgRed() *Builder { return b.chainStyle("\x1b[41m", "\x1b[49m") }

// BgRed styles text with the default instance (`chalk.bgRed(text)`).
func BgRed(text string) string { return Default().BgRed().Apply(text) }

// BgGreen starts a chain with the bgGreen background color style.
func (c *Chalk) BgGreen() *Builder { return c.chainStyle("\x1b[42m", "\x1b[49m") }

// BgGreen extends the chain with the bgGreen background color style.
func (b *Builder) BgGreen() *Builder { return b.chainStyle("\x1b[42m", "\x1b[49m") }

// BgGreen styles text with the default instance (`chalk.bgGreen(text)`).
func BgGreen(text string) string { return Default().BgGreen().Apply(text) }

// BgYellow starts a chain with the bgYellow background color style.
func (c *Chalk) BgYellow() *Builder { return c.chainStyle("\x1b[43m", "\x1b[49m") }

// BgYellow extends the chain with the bgYellow background color style.
func (b *Builder) BgYellow() *Builder { return b.chainStyle("\x1b[43m", "\x1b[49m") }

// BgYellow styles text with the default instance (`chalk.bgYellow(text)`).
func BgYellow(text string) string { return Default().BgYellow().Apply(text) }

// BgBlue starts a chain with the bgBlue background color style.
func (c *Chalk) BgBlue() *Builder { return c.chainStyle("\x1b[44m", "\x1b[49m") }

// BgBlue extends the chain with the bgBlue background color style.
func (b *Builder) BgBlue() *Builder { return b.chainStyle("\x1b[44m", "\x1b[49m") }

// BgBlue styles text with the default instance (`chalk.bgBlue(text)`).
func BgBlue(text string) string { return Default().BgBlue().Apply(text) }

// BgMagenta starts a chain with the bgMagenta background color style.
func (c *Chalk) BgMagenta() *Builder { return c.chainStyle("\x1b[45m", "\x1b[49m") }

// BgMagenta extends the chain with the bgMagenta background color style.
func (b *Builder) BgMagenta() *Builder { return b.chainStyle("\x1b[45m", "\x1b[49m") }

// BgMagenta styles text with the default instance (`chalk.bgMagenta(text)`).
func BgMagenta(text string) string { return Default().BgMagenta().Apply(text) }

// BgCyan starts a chain with the bgCyan background color style.
func (c *Chalk) BgCyan() *Builder { return c.chainStyle("\x1b[46m", "\x1b[49m") }

// BgCyan extends the chain with the bgCyan background color style.
func (b *Builder) BgCyan() *Builder { return b.chainStyle("\x1b[46m", "\x1b[49m") }

// BgCyan styles text with the default instance (`chalk.bgCyan(text)`).
func BgCyan(text string) string { return Default().BgCyan().Apply(text) }

// BgWhite starts a chain with the bgWhite background color style.
func (c *Chalk) BgWhite() *Builder { return c.chainStyle("\x1b[47m", "\x1b[49m") }

// BgWhite extends the chain with the bgWhite background color style.
func (b *Builder) BgWhite() *Builder { return b.chainStyle("\x1b[47m", "\x1b[49m") }

// BgWhite styles text with the default instance (`chalk.bgWhite(text)`).
func BgWhite(text string) string { return Default().BgWhite().Apply(text) }

// BgBlackBright starts a chain with the bgBlackBright background color style.
func (c *Chalk) BgBlackBright() *Builder { return c.chainStyle("\x1b[100m", "\x1b[49m") }

// BgBlackBright extends the chain with the bgBlackBright background color style.
func (b *Builder) BgBlackBright() *Builder { return b.chainStyle("\x1b[100m", "\x1b[49m") }

// BgBlackBright styles text with the default instance (`chalk.bgBlackBright(text)`).
func BgBlackBright(text string) string { return Default().BgBlackBright().Apply(text) }

// BgGray starts a chain with the bgGray background color style.
func (c *Chalk) BgGray() *Builder { return c.chainStyle("\x1b[100m", "\x1b[49m") }

// BgGray extends the chain with the bgGray background color style.
func (b *Builder) BgGray() *Builder { return b.chainStyle("\x1b[100m", "\x1b[49m") }

// BgGray styles text with the default instance (`chalk.bgGray(text)`).
func BgGray(text string) string { return Default().BgGray().Apply(text) }

// BgGrey starts a chain with the bgGrey background color style.
func (c *Chalk) BgGrey() *Builder { return c.chainStyle("\x1b[100m", "\x1b[49m") }

// BgGrey extends the chain with the bgGrey background color style.
func (b *Builder) BgGrey() *Builder { return b.chainStyle("\x1b[100m", "\x1b[49m") }

// BgGrey styles text with the default instance (`chalk.bgGrey(text)`).
func BgGrey(text string) string { return Default().BgGrey().Apply(text) }

// BgRedBright starts a chain with the bgRedBright background color style.
func (c *Chalk) BgRedBright() *Builder { return c.chainStyle("\x1b[101m", "\x1b[49m") }

// BgRedBright extends the chain with the bgRedBright background color style.
func (b *Builder) BgRedBright() *Builder { return b.chainStyle("\x1b[101m", "\x1b[49m") }

// BgRedBright styles text with the default instance (`chalk.bgRedBright(text)`).
func BgRedBright(text string) string { return Default().BgRedBright().Apply(text) }

// BgGreenBright starts a chain with the bgGreenBright background color style.
func (c *Chalk) BgGreenBright() *Builder { return c.chainStyle("\x1b[102m", "\x1b[49m") }

// BgGreenBright extends the chain with the bgGreenBright background color style.
func (b *Builder) BgGreenBright() *Builder { return b.chainStyle("\x1b[102m", "\x1b[49m") }

// BgGreenBright styles text with the default instance (`chalk.bgGreenBright(text)`).
func BgGreenBright(text string) string { return Default().BgGreenBright().Apply(text) }

// BgYellowBright starts a chain with the bgYellowBright background color style.
func (c *Chalk) BgYellowBright() *Builder { return c.chainStyle("\x1b[103m", "\x1b[49m") }

// BgYellowBright extends the chain with the bgYellowBright background color style.
func (b *Builder) BgYellowBright() *Builder { return b.chainStyle("\x1b[103m", "\x1b[49m") }

// BgYellowBright styles text with the default instance (`chalk.bgYellowBright(text)`).
func BgYellowBright(text string) string { return Default().BgYellowBright().Apply(text) }

// BgBlueBright starts a chain with the bgBlueBright background color style.
func (c *Chalk) BgBlueBright() *Builder { return c.chainStyle("\x1b[104m", "\x1b[49m") }

// BgBlueBright extends the chain with the bgBlueBright background color style.
func (b *Builder) BgBlueBright() *Builder { return b.chainStyle("\x1b[104m", "\x1b[49m") }

// BgBlueBright styles text with the default instance (`chalk.bgBlueBright(text)`).
func BgBlueBright(text string) string { return Default().BgBlueBright().Apply(text) }

// BgMagentaBright starts a chain with the bgMagentaBright background color style.
func (c *Chalk) BgMagentaBright() *Builder { return c.chainStyle("\x1b[105m", "\x1b[49m") }

// BgMagentaBright extends the chain with the bgMagentaBright background color style.
func (b *Builder) BgMagentaBright() *Builder { return b.chainStyle("\x1b[105m", "\x1b[49m") }

// BgMagentaBright styles text with the default instance (`chalk.bgMagentaBright(text)`).
func BgMagentaBright(text string) string { return Default().BgMagentaBright().Apply(text) }

// BgCyanBright starts a chain with the bgCyanBright background color style.
func (c *Chalk) BgCyanBright() *Builder { return c.chainStyle("\x1b[106m", "\x1b[49m") }

// BgCyanBright extends the chain with the bgCyanBright background color style.
func (b *Builder) BgCyanBright() *Builder { return b.chainStyle("\x1b[106m", "\x1b[49m") }

// BgCyanBright styles text with the default instance (`chalk.bgCyanBright(text)`).
func BgCyanBright(text string) string { return Default().BgCyanBright().Apply(text) }

// BgWhiteBright starts a chain with the bgWhiteBright background color style.
func (c *Chalk) BgWhiteBright() *Builder { return c.chainStyle("\x1b[107m", "\x1b[49m") }

// BgWhiteBright extends the chain with the bgWhiteBright background color style.
func (b *Builder) BgWhiteBright() *Builder { return b.chainStyle("\x1b[107m", "\x1b[49m") }

// BgWhiteBright styles text with the default instance (`chalk.bgWhiteBright(text)`).
func BgWhiteBright(text string) string { return Default().BgWhiteBright().Apply(text) }

// UnderlineBlack starts a chain with the underlineBlack underline color style.
func (c *Chalk) UnderlineBlack() *Builder { return c.chainStyle("\x1b[58;5;0m", "\x1b[59m") }

// UnderlineBlack extends the chain with the underlineBlack underline color style.
func (b *Builder) UnderlineBlack() *Builder { return b.chainStyle("\x1b[58;5;0m", "\x1b[59m") }

// UnderlineBlack styles text with the default instance (`chalk.underlineBlack(text)`).
func UnderlineBlack(text string) string { return Default().UnderlineBlack().Apply(text) }

// UnderlineRed starts a chain with the underlineRed underline color style.
func (c *Chalk) UnderlineRed() *Builder { return c.chainStyle("\x1b[58;5;1m", "\x1b[59m") }

// UnderlineRed extends the chain with the underlineRed underline color style.
func (b *Builder) UnderlineRed() *Builder { return b.chainStyle("\x1b[58;5;1m", "\x1b[59m") }

// UnderlineRed styles text with the default instance (`chalk.underlineRed(text)`).
func UnderlineRed(text string) string { return Default().UnderlineRed().Apply(text) }

// UnderlineGreen starts a chain with the underlineGreen underline color style.
func (c *Chalk) UnderlineGreen() *Builder { return c.chainStyle("\x1b[58;5;2m", "\x1b[59m") }

// UnderlineGreen extends the chain with the underlineGreen underline color style.
func (b *Builder) UnderlineGreen() *Builder { return b.chainStyle("\x1b[58;5;2m", "\x1b[59m") }

// UnderlineGreen styles text with the default instance (`chalk.underlineGreen(text)`).
func UnderlineGreen(text string) string { return Default().UnderlineGreen().Apply(text) }

// UnderlineYellow starts a chain with the underlineYellow underline color style.
func (c *Chalk) UnderlineYellow() *Builder { return c.chainStyle("\x1b[58;5;3m", "\x1b[59m") }

// UnderlineYellow extends the chain with the underlineYellow underline color style.
func (b *Builder) UnderlineYellow() *Builder { return b.chainStyle("\x1b[58;5;3m", "\x1b[59m") }

// UnderlineYellow styles text with the default instance (`chalk.underlineYellow(text)`).
func UnderlineYellow(text string) string { return Default().UnderlineYellow().Apply(text) }

// UnderlineBlue starts a chain with the underlineBlue underline color style.
func (c *Chalk) UnderlineBlue() *Builder { return c.chainStyle("\x1b[58;5;4m", "\x1b[59m") }

// UnderlineBlue extends the chain with the underlineBlue underline color style.
func (b *Builder) UnderlineBlue() *Builder { return b.chainStyle("\x1b[58;5;4m", "\x1b[59m") }

// UnderlineBlue styles text with the default instance (`chalk.underlineBlue(text)`).
func UnderlineBlue(text string) string { return Default().UnderlineBlue().Apply(text) }

// UnderlineMagenta starts a chain with the underlineMagenta underline color style.
func (c *Chalk) UnderlineMagenta() *Builder { return c.chainStyle("\x1b[58;5;5m", "\x1b[59m") }

// UnderlineMagenta extends the chain with the underlineMagenta underline color style.
func (b *Builder) UnderlineMagenta() *Builder { return b.chainStyle("\x1b[58;5;5m", "\x1b[59m") }

// UnderlineMagenta styles text with the default instance (`chalk.underlineMagenta(text)`).
func UnderlineMagenta(text string) string { return Default().UnderlineMagenta().Apply(text) }

// UnderlineCyan starts a chain with the underlineCyan underline color style.
func (c *Chalk) UnderlineCyan() *Builder { return c.chainStyle("\x1b[58;5;6m", "\x1b[59m") }

// UnderlineCyan extends the chain with the underlineCyan underline color style.
func (b *Builder) UnderlineCyan() *Builder { return b.chainStyle("\x1b[58;5;6m", "\x1b[59m") }

// UnderlineCyan styles text with the default instance (`chalk.underlineCyan(text)`).
func UnderlineCyan(text string) string { return Default().UnderlineCyan().Apply(text) }

// UnderlineWhite starts a chain with the underlineWhite underline color style.
func (c *Chalk) UnderlineWhite() *Builder { return c.chainStyle("\x1b[58;5;7m", "\x1b[59m") }

// UnderlineWhite extends the chain with the underlineWhite underline color style.
func (b *Builder) UnderlineWhite() *Builder { return b.chainStyle("\x1b[58;5;7m", "\x1b[59m") }

// UnderlineWhite styles text with the default instance (`chalk.underlineWhite(text)`).
func UnderlineWhite(text string) string { return Default().UnderlineWhite().Apply(text) }

// UnderlineBlackBright starts a chain with the underlineBlackBright underline color style.
func (c *Chalk) UnderlineBlackBright() *Builder { return c.chainStyle("\x1b[58;5;8m", "\x1b[59m") }

// UnderlineBlackBright extends the chain with the underlineBlackBright underline color style.
func (b *Builder) UnderlineBlackBright() *Builder { return b.chainStyle("\x1b[58;5;8m", "\x1b[59m") }

// UnderlineBlackBright styles text with the default instance (`chalk.underlineBlackBright(text)`).
func UnderlineBlackBright(text string) string { return Default().UnderlineBlackBright().Apply(text) }

// UnderlineGray starts a chain with the underlineGray underline color style.
func (c *Chalk) UnderlineGray() *Builder { return c.chainStyle("\x1b[58;5;8m", "\x1b[59m") }

// UnderlineGray extends the chain with the underlineGray underline color style.
func (b *Builder) UnderlineGray() *Builder { return b.chainStyle("\x1b[58;5;8m", "\x1b[59m") }

// UnderlineGray styles text with the default instance (`chalk.underlineGray(text)`).
func UnderlineGray(text string) string { return Default().UnderlineGray().Apply(text) }

// UnderlineGrey starts a chain with the underlineGrey underline color style.
func (c *Chalk) UnderlineGrey() *Builder { return c.chainStyle("\x1b[58;5;8m", "\x1b[59m") }

// UnderlineGrey extends the chain with the underlineGrey underline color style.
func (b *Builder) UnderlineGrey() *Builder { return b.chainStyle("\x1b[58;5;8m", "\x1b[59m") }

// UnderlineGrey styles text with the default instance (`chalk.underlineGrey(text)`).
func UnderlineGrey(text string) string { return Default().UnderlineGrey().Apply(text) }

// UnderlineRedBright starts a chain with the underlineRedBright underline color style.
func (c *Chalk) UnderlineRedBright() *Builder { return c.chainStyle("\x1b[58;5;9m", "\x1b[59m") }

// UnderlineRedBright extends the chain with the underlineRedBright underline color style.
func (b *Builder) UnderlineRedBright() *Builder { return b.chainStyle("\x1b[58;5;9m", "\x1b[59m") }

// UnderlineRedBright styles text with the default instance (`chalk.underlineRedBright(text)`).
func UnderlineRedBright(text string) string { return Default().UnderlineRedBright().Apply(text) }

// UnderlineGreenBright starts a chain with the underlineGreenBright underline color style.
func (c *Chalk) UnderlineGreenBright() *Builder { return c.chainStyle("\x1b[58;5;10m", "\x1b[59m") }

// UnderlineGreenBright extends the chain with the underlineGreenBright underline color style.
func (b *Builder) UnderlineGreenBright() *Builder { return b.chainStyle("\x1b[58;5;10m", "\x1b[59m") }

// UnderlineGreenBright styles text with the default instance (`chalk.underlineGreenBright(text)`).
func UnderlineGreenBright(text string) string { return Default().UnderlineGreenBright().Apply(text) }

// UnderlineYellowBright starts a chain with the underlineYellowBright underline color style.
func (c *Chalk) UnderlineYellowBright() *Builder { return c.chainStyle("\x1b[58;5;11m", "\x1b[59m") }

// UnderlineYellowBright extends the chain with the underlineYellowBright underline color style.
func (b *Builder) UnderlineYellowBright() *Builder { return b.chainStyle("\x1b[58;5;11m", "\x1b[59m") }

// UnderlineYellowBright styles text with the default instance (`chalk.underlineYellowBright(text)`).
func UnderlineYellowBright(text string) string { return Default().UnderlineYellowBright().Apply(text) }

// UnderlineBlueBright starts a chain with the underlineBlueBright underline color style.
func (c *Chalk) UnderlineBlueBright() *Builder { return c.chainStyle("\x1b[58;5;12m", "\x1b[59m") }

// UnderlineBlueBright extends the chain with the underlineBlueBright underline color style.
func (b *Builder) UnderlineBlueBright() *Builder { return b.chainStyle("\x1b[58;5;12m", "\x1b[59m") }

// UnderlineBlueBright styles text with the default instance (`chalk.underlineBlueBright(text)`).
func UnderlineBlueBright(text string) string { return Default().UnderlineBlueBright().Apply(text) }

// UnderlineMagentaBright starts a chain with the underlineMagentaBright underline color style.
func (c *Chalk) UnderlineMagentaBright() *Builder { return c.chainStyle("\x1b[58;5;13m", "\x1b[59m") }

// UnderlineMagentaBright extends the chain with the underlineMagentaBright underline color style.
func (b *Builder) UnderlineMagentaBright() *Builder { return b.chainStyle("\x1b[58;5;13m", "\x1b[59m") }

// UnderlineMagentaBright styles text with the default instance (`chalk.underlineMagentaBright(text)`).
func UnderlineMagentaBright(text string) string {
	return Default().UnderlineMagentaBright().Apply(text)
}

// UnderlineCyanBright starts a chain with the underlineCyanBright underline color style.
func (c *Chalk) UnderlineCyanBright() *Builder { return c.chainStyle("\x1b[58;5;14m", "\x1b[59m") }

// UnderlineCyanBright extends the chain with the underlineCyanBright underline color style.
func (b *Builder) UnderlineCyanBright() *Builder { return b.chainStyle("\x1b[58;5;14m", "\x1b[59m") }

// UnderlineCyanBright styles text with the default instance (`chalk.underlineCyanBright(text)`).
func UnderlineCyanBright(text string) string { return Default().UnderlineCyanBright().Apply(text) }

// UnderlineWhiteBright starts a chain with the underlineWhiteBright underline color style.
func (c *Chalk) UnderlineWhiteBright() *Builder { return c.chainStyle("\x1b[58;5;15m", "\x1b[59m") }

// UnderlineWhiteBright extends the chain with the underlineWhiteBright underline color style.
func (b *Builder) UnderlineWhiteBright() *Builder { return b.chainStyle("\x1b[58;5;15m", "\x1b[59m") }

// UnderlineWhiteBright styles text with the default instance (`chalk.underlineWhiteBright(text)`).
func UnderlineWhiteBright(text string) string { return Default().UnderlineWhiteBright().Apply(text) }
