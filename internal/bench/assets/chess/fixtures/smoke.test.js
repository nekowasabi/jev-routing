import assert from "node:assert/strict";
import { test } from "node:test";
import { Chess } from "../src/chess.js";

test("the starting position has 20 legal moves", () => {
  assert.equal(new Chess().moves().length, 20);
});

test("FEN round-trips", () => {
  const fen = "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1";
  assert.equal(new Chess(fen).fen(), fen);
});
