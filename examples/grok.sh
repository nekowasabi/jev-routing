#!/bin/sh
# Grok Build を Jev プロキシ経由で起動する。npx も mcp add も使わない。
export JEV_AUTO_APPLY="${JEV_AUTO_APPLY:-1}"
exec jev-routing run grok -- "$@"
