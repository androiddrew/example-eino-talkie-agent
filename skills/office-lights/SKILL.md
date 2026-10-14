---
name: office-lights
description: Turn the office lights on or off, make them warm or cool, or set them to half or full brightness.
---
Control the office lights through Home Assistant by calling the
office-lights tool with one command in args:

| The user asks to | args |
|---|---|
| turn the lights on | ["on"] |
| turn the lights off | ["off"] |
| make them warm, for the morning or evening (3000 K) | ["morning"] |
| make them cool, daylight (5400 K) | ["day"] |
| dim them, half brightness | ["half"] |
| make them bright, full brightness | ["full"] |

For a request that needs two commands, such as "on and warm", call the tool
once for each.

The tool returns exit code 0 when it worked. A non-zero exit code or an
"Error:" line means it failed: tell the user briefly.
