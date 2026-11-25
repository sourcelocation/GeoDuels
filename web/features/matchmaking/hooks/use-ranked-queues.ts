import { useQuery } from "@tanstack/react-query";
import { fetchRankedQueues, type QueueVariant, type RankedQueue } from "../lib/queue-client";

/** The ranked queues the matchmaker offers, empty until they've loaded. */
export function useRankedQueues(): RankedQueue[] {
  const query = useQuery({
    queryKey: ["ranked-queues"],
    queryFn: ({ signal }) => fetchRankedQueues(signal),
    staleTime: 60_000,
  });
  return query.data ?? [];
}

/**
 * The queues to wait in: the saved ones the matchmaker still offers, or the first it offers when none
 * of them are.
 */
export function chosenQueues(saved: QueueVariant[], offered: RankedQueue[]): QueueVariant[] {
  const chosen = saved.filter((id) => offered.some((queue) => queue.id === id));
  if (chosen.length || !offered.length) return chosen;
  return [offered[0].id];
}
