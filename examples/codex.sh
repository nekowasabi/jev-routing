#!/bin/sh
export JEV_AUTO_APPLY="${JEV_AUTO_APPLY:-1}"
exec jev-routing run codex -- "$@"
