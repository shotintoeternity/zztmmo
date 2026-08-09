// moderation.ts — the operator's half of the Players window (M21.2).
//
// M21.1's window offers one thing: block, which changes what YOU hear. An
// operator gets three more, which change what everybody hears — mute, kick and
// refuse — so picking a person opens a menu of actions instead of a single
// yes/no question.
//
// Pure logic, free of the DOM and of module state (the blocks.ts pattern):
// main.ts owns the window, the keys and the socket, and the SERVER owns every
// one of these actions. Nothing here mutes, kicks or refuses anybody, and
// nothing here could — a client that puts `operator: true` on itself gains
// exactly nothing, because the allowlist is checked again on every action.
//
// Two things this file exists to say out loud:
//
//  1. **A non-operator gets no sanctions.** moderationChoices returns an empty
//     list for them; main.ts may still open an action menu for profile/block
//     choices, but the operator-only rows never appear.
//  2. **Refuse is honest about its limit.** A refusal binds to an account, and
//     the client cannot tell a signed-in player from a guest — so the row says
//     what refuse does and does not reach, rather than promising a guest is gone
//     for good and letting the operator find out otherwise.

export type ModerationAction = "block" | "unblock" | "mute" | "unmute" | "kick" | "refuse";

export type ModerationChoice = {
  action: ModerationAction;
  /** The row in the action list, and the value the text window hands back. */
  label: string;
  /** The yes/no question asked before it is sent. */
  confirm: string;
};

/** The select list's inner span; a row must fit or it wraps into the border. */
const ROW_WIDTH = 40;

/**
 * moderationChoices is what an operator may do to one person, mildest first.
 *
 * The order is deliberate: an operator scrolling this list passes the reversible
 * sanctions before reaching the ones that end somebody's session, and the row
 * that cannot be undone from inside the game is last.
 *
 * `muted` is what this client last heard the server say about the target, and
 * both directions are always offered anyway — an operator who has just arrived
 * knows nothing about who is muted, and a toggle computed from that nothing
 * would invert the wrong way. Stating the action rather than toggling one is the
 * same choice M21.1 made for block.
 */
export function moderationChoices(
  target: { name: string; id: number; blocked: boolean; muted?: boolean },
  operator: boolean,
): ModerationChoice[] {
  if (!operator) {
    return [];
  }
  const who = target.name || "that player";
  const choices: ModerationChoice[] = [
    target.blocked
      ? { action: "unblock", label: "Unblock (for me only)", confirm: `Unblock ${who}? ` }
      : { action: "block", label: "Block (for me only)", confirm: `Block ${who}? ` },
    { action: "mute", label: "Mute in chat", confirm: `Mute ${who} in chat? ` },
    { action: "unmute", label: "Unmute", confirm: `Unmute ${who}? ` },
    { action: "kick", label: "Kick (they may return)", confirm: `Kick ${who}? ` },
    { action: "refuse", label: "Refuse (guests can return)", confirm: `Refuse ${who} for good? ` },
  ];
  return choices.map((choice) => ({ ...choice, label: choice.label.slice(0, ROW_WIDTH) }));
}

/**
 * The header the action list opens with. It carries the one limit an operator
 * cannot see for themselves — whether the person in front of them is signed in —
 * because that is what decides whether "refuse" means anything at all.
 */
export function moderationHeader(target: { name: string; id: number }): string[] {
  return [
    "",
    `  ${(target.name || "that player").slice(0, 20)} #${target.id}`,
    "  Mute and kick reach anybody here.",
    "  Refuse holds only against a signed-in",
    "  account; a guest can come back.",
    "",
  ];
}
