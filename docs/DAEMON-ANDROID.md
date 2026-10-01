# Keeping `lrm daemon` alive on Android

Android does not let a background process run indefinitely. The system
suspends and then kills whatever it considers idle, most aggressively once
the screen is off, and there is **no way for an ordinary process to opt
out**. This is a platform policy, not a bug in LRM, and it cannot be fully
solved from inside the program.

Three things help. LRM Mobile can do two of them for you.

## 1. Exempt Termux from battery optimisation (you must do this)

This is the single most effective change, and only you can make it:

**Settings → Apps → Termux → Battery → Unrestricted**

Wording varies by manufacturer. Samsung, Xiaomi, Huawei and OnePlus are
notoriously aggressive and often have a *second* switch buried elsewhere —
search your settings for "battery optimisation", "app launch", "auto-start"
or "protected apps".

## 2. Hold a wake lock

```sh
pkg install termux-api      # plus the Termux:API app from F-Droid
lrm daemon --wake-lock
```

This keeps the CPU from suspending while the daemon runs, and releases the
lock when you stop it. **It will drain your battery faster** — that is what
it is for, which is why it is opt-in rather than default.

## 3. Restart automatically when the system kills it

```sh
lrm daemon --supervise
```

LRM's sync is resumable by design, so a daemon that comes back loses
nothing but time. The supervisor is the difference between "sync stopped
three hours ago" and "sync paused for forty seconds".

It re-executes the binary rather than restarting in-process, because a
killed daemon may have left the runtime in any state — and on Android the
kill usually takes the whole process anyway. Restarts back off from one
second to a maximum of sixty, and the backoff resets whenever the daemon
managed to run for more than thirty seconds, so a system kill comes
straight back while a genuine configuration error (bad port, no repo) does
not spin.

```sh
lrm daemon --supervise --max-restarts 20   # give up eventually
```

## Recommended setup

```sh
pkg install tmux termux-api
tmux
lrm daemon --wake-lock --supervise
```

Then detach with `Ctrl-B D`. The tmux session survives Termux being
backgrounded; reattach with `tmux attach`.

## What is honestly not solved

- **Doze mode.** With the screen off for a long period, Android batches
  network access into maintenance windows. A wake lock keeps the CPU up but
  does not exempt you from Doze's network restrictions. Expect sync to be
  bursty rather than real-time when the phone is idle overnight.
- **The system can still kill the supervisor.** It is a process too. If
  Android reclaims the whole Termux session, nothing inside it survives.
  Battery-optimisation exemption is what reduces this.
- **No boot persistence.** Starting the daemon at boot needs Termux:Boot
  (a separate F-Droid app). Not wired up yet.
- **None of this has been measured over a long period on a real device.**
  The mechanisms are implemented and unit-tested; how well they hold up
  across manufacturers and Android versions is exactly the kind of claim
  this project refuses to make without evidence. See `TESTING.md`.

## If supervision gives up

```
lrm: the daemon exited immediately 3 times in a row (exit status 1).
lrm: that is a startup failure, not Android reclaiming the process,
lrm: so supervision is giving up rather than looping.
```

This is deliberate. A daemon that dies in under five seconds with an
ordinary exit status was not killed by the system — it failed to start,
and restarting it cannot help. Earlier versions looped on a doubling
backoff and buried the real error in their own output. The error you need
is printed immediately above that message; the usual causes are running
outside an LRM repository or a port already in use.

A daemon killed by a signal, or one that ran for a while first, is treated
as a system kill and restarted as normal.

## Diagnosing

```sh
lrm platform      # confirm the environment
lrm status        # is the daemon's view current?
lrm watch         # live daemon events
```

If the daemon dies immediately rather than after a while, it is not
Android — check the port is free and you are inside an LRM repository.
