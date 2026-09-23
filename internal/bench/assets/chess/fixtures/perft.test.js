import assert from "node:assert/strict";
import { test } from "node:test";
import { Chess } from "../src/chess.js";

// perft(n) counts every sequence of n legal moves from a position. The expected numbers are the
// published ones, so any rule the engine gets wrong shows up as a different count.
function perft(fen, depth) {
  const moves = new Chess(fen).moves();
  if (depth === 1) return moves.length;
  let nodes = 0;
  for (const move of moves) {
    const next = new Chess(fen);
    next.move(move);
    nodes += perft(next.fen(), depth - 1);
  }
  return nodes;
}

const POSITIONS = [
  ["start", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", [20, 400, 8902]],
  ["kiwipete", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", [48, 2039]],
  ["endgame", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", [14, 191, 2812]],
  ["promotions", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", [6, 264]],
  ["promotion with check", "rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8", [44, 1486]],
];

for (const [name, fen, counts] of POSITIONS) {
  counts.forEach((expected, i) => {
    test(`perft ${i + 1} from ${name}`, () => assert.equal(perft(fen, i + 1), expected));
  });
}

test("capturing a rook on its home square removes that castling right", () => {
  const game = new Chess("r3k2r/8/8/8/8/8/6B1/R3K2R w KQkq - 0 1");
  game.move("g2a8");
  assert.equal(game.fen().split(" ")[2], "KQk");
});
