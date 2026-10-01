# Records the showcase in docs/images: the GIF at the top of the README
# and one screenshot per screen. Run from the repository root:
#
#   brew install vhs        # or: go install github.com/charmbracelet/vhs@latest
#   vhs docs/demo.tape
#
# The console runs on synthetic vCenters (rightsizer demo); nothing is
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
Set TypingSpeed 70ms
Set Theme { "name": "Twilight", "background": "#13111E", "foreground": "#E9E8F7", "selection": "#3D0E61", "cursor": "#97DFFC", "cursorAccent": "#13111E", "black": "#1D1934", "red": "#F87171", "green": "#4ADE80", "yellow": "#FBBF24", "blue": "#8EB5F0", "magenta": "#858AE3", "cyan": "#97DFFC", "white": "#E9E8F7", "brightBlack": "#5D5791", "brightRed": "#FCA5A5", "brightGreen": "#86EFAC", "brightYellow": "#FDE68A", "brightBlue": "#93CAF6", "brightMagenta": "#A78BFA", "brightCyan": "#BAE6FD", "brightWhite": "#FFFFFF" }

Hide
Type "test -x ./rightsizer || make build >/dev/null; clear"
Enter
Sleep 2s
Show

Type "./rightsizer demo"
Sleep 500ms
Enter
Sleep 2.5s
Screenshot docs/images/home.png

Down
Sleep 700ms
Up
Sleep 700ms
Enter
Sleep 2s
Screenshot docs/images/overview.png

Type "2"
Sleep 1s
Down 4
Sleep 1s
Screenshot docs/images/findings.png
Type "/"
Sleep 400ms
Type "db"
Sleep 800ms
Enter
Sleep 1.2s

Type "3"
Sleep 2s
Screenshot docs/images/peaks.png
Down 6
Sleep 1s

Type "4"
Sleep 2.5s
Screenshot docs/images/sizing.png
Down 8
Sleep 1.2s

Escape
Sleep 1s
Type "a"
Sleep 800ms
Type "vcsa03.corp.local"
Sleep 400ms
Tab
Type "svc-rightsizer@vsphere.local"
Sleep 400ms
Tab
Type "correct horse battery"
Sleep 400ms
Tab
Tab
Right
Sleep 600ms
Screenshot docs/images/add.png
Sleep 1s
Escape
Sleep 800ms
Type "q"
Sleep 500ms
