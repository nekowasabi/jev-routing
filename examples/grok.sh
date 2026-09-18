#!/bin/sh
# Grok Build を Jev プロキシ経由で起動する。npx も mcp add も使わない。
exec jev-routing run grok -- "$@"
