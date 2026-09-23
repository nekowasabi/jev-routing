#!/bin/bash

for t in compact=off filter=off; do
  JEV_TRANSFORMS=$t jev-routing bench --agent codex --model gpt-5.6-terra --reps 3 --prices 1.25,0.125,10 --modes on
done
