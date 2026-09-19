import { useQuery } from "@tanstack/react-query";
import { getMeOptions, getMetaOptions } from "@/lib/api/@tanstack/react-query.gen";

// useMe is the caller: the local user, a signed-in user, a guest, or nobody (SDD §3).
export function useMe() {
  return useQuery({ ...getMeOptions(), staleTime: 30_000 });
}

export function useMeta() {
  return useQuery({ ...getMetaOptions(), staleTime: Infinity });
}
