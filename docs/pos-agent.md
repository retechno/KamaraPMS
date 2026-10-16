# The print agent (`pos-agent`)

The print agent is a small program that runs on a computer of the hotel (the cashier's, or any machine that can reach the printers). It calls the
server over HTTPS, takes the print jobs of the printers assigned to it and writes the bytes it is given to the printer, as they are. It has no
database, listens on no port and decides nothing about what is on the paper: the server writes the document and renders it
(`docs/architecture/34-pos.md` sections 10.3 and 20).

One agent serves all the printers that are assigned to its device. A hotel normally runs one agent for the receipt printers and the kitchen
printers; a second one is only needed when a printer cannot be reached from the first computer.

## 1. What you need

- The address of the server, for example `https://pms.example.com` (TLS only; `http://localhost` is accepted for a development server).
- The permission `pos.manage` in the application, to make the enrolment code and to assign printers.
- The printers, each with its address:

| Printer | `connection_info` | Notes |
|---|---|---|
| Network (Ethernet, Wi-Fi) | `192.168.1.50:9100` (port 9100 if left out) | raw TCP; give the printer a fixed address |
| USB, on Windows | `USB:EPSON TM-T82` | the name of the printer **as Windows shows it**; printed RAW through the spooler (no driver rendering) |
| USB, on Linux | `DEV:/dev/usb/lp0` | the account of the agent must be in the group `lp` |

Receipt printers are 58 mm (32 characters a line) or 80 mm (48). The code page is set per printer in Setup; `CP437` is right for most.

## 2. Installing on Windows

1. Copy `pos-agent.exe` to the computer, for example `C:\Program Files\KamaraPMS\pos-agent.exe`. There is no installer and no automatic update: a
   new version is copied over the old one and the service restarted.
2. In the application, **Setup > Stations and printers > Devices > Make an enrolment code**. The code (`K7QM-4TXD`) is shown once, works once
   and lives 15 minutes. The dialog also shows the command with the address of your server.
3. In an **administrator** PowerShell on the computer:

```powershell
cd "C:\Program Files\KamaraPMS"
.\pos-agent.exe enroll -server https://pms.example.com -code K7QM-4TXD -name "Kasir Lobby"
.\pos-agent.exe install
.\pos-agent.exe start
```

`enroll` writes `%ProgramData%\KamaraPMS\pos-agent\config.json` (the folder is limited to SYSTEM, the administrators and the account that
enrolled). The token in it is never printed to the screen or to the log. `install` registers the service `KamaraPMSPrintAgent` (starts with
Windows, restarts itself 30 seconds and one minute after a failure); `uninstall`, `start` and `stop` do what they say.

4. In **Setup > Stations and printers**, open each printer and choose the device (**Device (print agent)**). Its state changes from "Not reported" to
   "Ready" within half a minute.

## 3. Installing on Linux

```bash
sudo install -m 0755 pos-agent /usr/local/bin/pos-agent
sudo useradd --system --home /var/lib/kamarapms-agent --shell /usr/sbin/nologin --groups lp kamarapms-agent
sudo install -d -o kamarapms-agent -m 0700 /etc/kamarapms/pos-agent
sudo -u kamarapms-agent pos-agent enroll -server https://pms.example.com -code K7QM-4TXD -name "Kasir Lobby" \
     -config /etc/kamarapms/pos-agent/config.json
sudo cp deploy/pos-agent.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now pos-agent
journalctl -u pos-agent -f
```

The unit sets `POS_AGENT_CONFIG` and does not restart the program after exit status 1, which is how it ends when the server no longer accepts its
token (see 6).

## 4. Checking it

```text
pos-agent status                      the server, the token, and every printer with what it says: ONLINE, OFFLINE, PAPER_OUT, COVER_OPEN
pos-agent test-print "Kitchen"        prints a test page (name, width ruler, cut) on a printer, by name or number
pos-agent version
```

`status` and `test-print` read the same configuration file as the service (`-config FILE` or the variable `POS_AGENT_CONFIG` to point to another).
The log is `agent.log` beside the configuration (1 MB, three old files kept).

## 5. How it works

- The agent asks `GET /api/v1/device/jobs/next?wait=25`. The server holds the request until a job is ready or 25 seconds pass. A job it hands out
  is leased for 60 seconds: if the agent does not report in time, the job goes back to the queue and its attempt is counted.
- The jobs of one printer are handed out **in order**. A failed job is tried again after 10 seconds, then after 60 seconds, and at the third failure
  it is final: the cashier sees it with **Reprint**. A reprint is a new job marked `COPY n`; the original is never changed.
- Every 30 seconds it sends a heartbeat with the status of each printer (asking the printer for its paper and cover sensors with `DLE EOT`;
  a printer that does not answer is taken as online if it can be reached). A printer that has not been heard of for 90 seconds is shown as
  **Printer offline** in Setup and on the cashier screen.
- It never opens a port, and nothing the server sends makes it run anything: the only thing it does with a document is write the bytes.
- When the network breaks it waits (1 second, doubling to 60) and tries again; the jobs wait on the server. If the subscription of the hotel is
  read-only, the server refuses the claim and the agent waits a minute and asks again.

## 6. When something is wrong

| What you see | Meaning | What to do |
|---|---|---|
| Setup shows the device **Offline** | no heartbeat for 90 seconds: the computer is off, the service stopped, or the network to the server is down | `pos-agent status` on the computer; on Windows start the service `KamaraPMSPrintAgent`; on Linux `journalctl -u pos-agent` |
| "Printer offline" for one printer, the device is online | the agent cannot reach that printer (cable, power, wrong address) | check the address in `connection_info`; `pos-agent test-print "<printer>"`; for USB on Windows the name must be the Windows name |
| "Out of paper" / "Cover open" | the printer says so | fix it; the queued jobs print by themselves |
| The agent stops and the log says "the server does not accept this device token" | the device was revoked, or its token was replaced | enrol again with a new code (step 2); `enroll` replaces the configuration |
| `ENROLLMENT_CODE_INVALID` at `enroll` | the code was used, is older than 15 minutes, or was mistyped | make a new code |
| A job FAILED with `UNSUPPORTED_CONNECTION` | `USB:` on a Linux agent, or an empty `connection_info` | use `DEV:/dev/usb/lp0` or the IP address |
| Receipts print but the drawer does not open | the receipt printer is not set to open a drawer, or the drawer is on the other pin | tick **Opens the cash drawer** on the printer; most drawers use the first pin |
| Print stops while the application still works | the subscription is read-only (`SUBSCRIPTION_READ_ONLY` in the log) | the jobs wait; they print when the subscription is paid |
| Characters are wrong (accents) | the code page does not match the printer | the agent sends ASCII only (accents are written without them); if a printer prints other symbols, set its code page to `CP437` or `CP858` |
| The log says "polling failed ... retry_in" | the network or the server is not reachable | nothing is lost; check the internet connection of the computer |

The browser always remains a way to print: **Print from the browser** on a bill prints the receipt through the browser's print dialog when the
agent or the printer is down.

## 7. Security

- The token (`kpd_` and 32 random bytes) is shown once, stored on the server only as a SHA-256, and kept in `config.json` readable by the account
  of the agent. A token can be replaced (**Replace token**) or revoked (**Revoke**) in Setup at any time; the old one stops at once.
- The routes of a device accept no token of a user and the routes of a user accept no token of a device. A device can read and report only the jobs of
  its own printers. The device routes are limited per token.
- The documents (names, amounts) are removed from the server seven days after the job; the row (what, when, how it ended) stays.
- TLS only. The agent refuses an `http://` address except for `localhost`.

## 8. Building

```bash
GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=0.1.0" -o pos-agent.exe ./cmd/pos-agent
GOOS=linux   GOARCH=amd64 go build -ldflags "-X main.version=0.1.0" -o pos-agent     ./cmd/pos-agent
```

One binary, no cgo. The Windows spooler is called through `golang.org/x/sys/windows`. The CI builds both.
