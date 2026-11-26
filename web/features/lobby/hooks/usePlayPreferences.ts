import { useEffect, useState } from "react";
import type {
  GameRuleset,
  QueueVariant,
  StreetNamesVisibility,
} from "../../matchmaking/lib/queue-client";

type DuelPreferences = {
  queues: QueueVariant[];
};

type SingleplayerPreferences = {
  mode: GameRuleset;
  streetNames: StreetNamesVisibility;
};

const DUEL_STORAGE_KEY = "geoduels.play.duels";
const SINGLEPLAYER_STORAGE_KEY = "geoduels.play.singleplayer";

const supportedModes = new Set<GameRuleset>(["moving", "no_move", "nmpz"]);

// The queues a player chose, as they saved them. Which of them the matchmaker still offers is
// decided when they play (chosenQueues).
function parseRankedQueues(value: unknown): QueueVariant[] {
  if (!Array.isArray(value)) return [];
  return Array.from(
    new Set(value.filter((item): item is QueueVariant => typeof item === "string" && item !== "")),
  );
}

function readPreferences() {
  let duel: DuelPreferences = { queues: [] };
  let singleplayer: SingleplayerPreferences = {
    mode: "moving",
    streetNames: "shown",
  };
  try {
    const storedDuel = JSON.parse(
      window.localStorage.getItem(DUEL_STORAGE_KEY) || "null",
    );
    if (storedDuel && typeof storedDuel === "object") {
      duel = { queues: parseRankedQueues(storedDuel.queues) };
    }

    const storedSingleplayer = JSON.parse(
      window.localStorage.getItem(SINGLEPLAYER_STORAGE_KEY) || "null",
    );
    if (
      storedSingleplayer &&
      typeof storedSingleplayer === "object" &&
      supportedModes.has(storedSingleplayer.mode)
    ) {
      singleplayer = {
        mode: storedSingleplayer.mode,
        streetNames:
          storedSingleplayer.streetNames === "hidden" ? "hidden" : "shown",
      };
    }
  } catch {
    // Defaults keep the launcher usable when storage is unavailable or invalid.
  }
  return { duel, singleplayer };
}

export function usePlayPreferences() {
  const [initial] = useState(readPreferences);
  const [duel, setDuel] = useState<DuelPreferences>(initial.duel);
  const [singleplayer, setSingleplayer] = useState<SingleplayerPreferences>(
    initial.singleplayer,
  );

  useEffect(() => {
    try {
      window.localStorage.setItem(DUEL_STORAGE_KEY, JSON.stringify(duel));
    } catch {
      // Storage failures do not block launching.
    }
  }, [duel]);

  useEffect(() => {
    try {
      window.localStorage.setItem(
        SINGLEPLAYER_STORAGE_KEY,
        JSON.stringify(singleplayer),
      );
    } catch {
      // Storage failures do not block launching.
    }
  }, [singleplayer]);

  return { duel, setDuel, singleplayer, setSingleplayer };
}
