# Records the showcase in docs/images: the GIF at the top of the README
# and one screenshot per screen. Run from the repository root:
#
#   brew install vhs        # or: go install github.com/charmbracelet/vhs@latest
#   vhs docs/demo.tape
#
# The console runs on synthetic vCenters: a hidden step makes the
# recorded "rightsizer" command run "./rightsizer demo", so nothing is
# contacted.

Output docs/images/demo.gif

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 15
Set LineHeight 1.15
Set Width 1440
Set Height 860
Set Padding 18
Set Margin 36
Set MarginFill "#3D0E61"
Set BorderRadius 12
Set WindowBar Colorful
Set Framerate 30
Set TypingSpeed 110ms
Set Theme { "name": "Twilight", "background": "#13111E", "foreground": "#E9E8F7", "selection": "#3D0E61", "cursor": "#97DFFC", "cursorAccent": "#13111E", "black": "#1D1934", "red": "#F87171", "green": "#4ADE80", "yellow": "#FBBF24", "blue": "#8EB5F0", "magenta": "#858AE3", "cyan": "#97DFFC", "white": "#E9E8F7", "brightBlack": "#5D5791", "brightRed": "#FCA5A5", "brightGreen": "#86EFAC", "brightYellow": "#FDE68A", "brightBlue": "#93CAF6", "brightMagenta": "#A78BFA", "brightCyan": "#BAE6FD", "brightWhite": "#FFFFFF" }

Hide
Type "test -x ./rightsizer || make build >/dev/null; rightsizer() { ./rightsizer demo; }; clear"
Enter
Sleep 2s
Show

Sleep 1s
Type "rightsizer"
Sleep 800ms
Enter
Sleep 4s
Screenshot docs/images/home.png

Down
Sleep 1.2s
Up
Sleep 1.2s
Enter
Sleep 4.5s
Screenshot docs/images/overview.png

Type "2"
Sleep 2s
Down@400ms 4
Sleep 2.5s
Screenshot docs/images/findings.png
Type "/"
Sleep 700ms
Type@180ms "db"
Sleep 1.2s
Enter
Sleep 2.5s

Type "3"
Sleep 4s
Screenshot docs/images/peaks.png
Down@250ms 6
Sleep 2s

Type "4"
Sleep 4s
Screenshot docs/images/sizing.png
Down@250ms 8
Sleep 2.5s

Escape
Sleep 2s
Type "a"
Sleep 1.5s
Type "vcsa03.corp.local"
Sleep 700ms
Tab
Type "svc-rightsizer@vsphere.local"
Sleep 700ms
Tab
Type "correct horse battery"
Sleep 700ms
Tab
Sleep 600ms
Tab
Sleep 600ms
Right
Sleep 1.5s
Screenshot docs/images/add.png
Sleep 2s
Escape
Sleep 1.5s
Type "q"
Sleep 1s
