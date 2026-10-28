# Records docs/demo.gif with charmbracelet/vhs: `make demo`.
# A short k6 run of the echobot scenario against the real mock.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 13
Set Width 860
Set Height 590
Set Padding 18
Set TypingSpeed 45ms
Set Framerate 20
Set Theme { "name": "telegym", "background": "#0b0f14", "foreground": "#e6edf3", "cursor": "#29a1d4", "selection": "#1f2630", "black": "#0b0f14", "red": "#ff7b72", "green": "#7ee787", "yellow": "#f0a13a", "blue": "#29a1d4", "magenta": "#d2a8ff", "cyan": "#29a1d4", "white": "#e6edf3", "brightBlack": "#6b7785", "brightRed": "#ff7b72", "brightGreen": "#7ee787", "brightYellow": "#f0a13a", "brightBlue": "#3ab4e7", "brightMagenta": "#d2a8ff", "brightCyan": "#3ab4e7", "brightWhite": "#ffffff" }

# Off camera: put bin/ on PATH and move next to the scenarios.
Hide
Type "export PS1='$ ' PATH=$PWD/bin:$PATH K6_LOG_OUTPUT=none K6_SUMMARY_TREND_STATS='avg,p(95)'; cd examples/echobot; docker pull -q ghcr.io/kolomiichenko/telegym-mock >/dev/null; clear"
Enter
Sleep 4s
Show

# 1. The mock, straight from GHCR.
Type "docker run -d --name telegym -p 5678:5678 ghcr.io/kolomiichenko/telegym-mock"
Sleep 300ms
Enter
Sleep 2s

# 2. The bot under test: polling, so it reaches the mock in Docker as is.
Type "echobot -poll & sleep 1"
Sleep 300ms
Enter
Sleep 2s

# 3. Load: 20 virtual users for 8 seconds.
Type "k6 run --vus 20 --duration 8s scenarios/echo.js"
Sleep 300ms
Enter
Sleep 11s
Sleep 5s

Hide
Type "kill %1; docker rm -f telegym >/dev/null; clear"
Enter
