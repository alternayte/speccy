import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { ErrorState, Loading } from "@/components/ui/states";
import {
  deleteNotificationsMutation,
  getNotificationsOptions,
  getNotificationsQueryKey,
  setNotificationsMutation,
  testNotificationsMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { Section } from "./admin-page";

// NotificationsSection sets the one Slack or Teams channel that hears what waits for people
// (docs/specs/channel-notifications.md). Hosted mode only.
export function NotificationsSection() {
  const qc = useQueryClient();
  const state = useQuery(getNotificationsOptions());
  const [kind, setKind] = useState<"slack" | "teams">("slack");
  const [url, setUrl] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: getNotificationsQueryKey() });
  const save = useMutation({
    ...setNotificationsMutation(),
    onSuccess: () => {
      setUrl("");
      refresh();
    },
  });
  const remove = useMutation({ ...deleteNotificationsMutation(), onSuccess: refresh });
  const test = useMutation(testNotificationsMutation());
  if (state.isPending) return <Loading label="Loading the notifications" />;
  if (state.isError) return <ErrorState message={problemMessage(state.error)} />;
  const w = state.data;
  return (
    <Section title="Notifications">
      <div className="space-y-3 px-4 py-4 text-sm">
        <p className="text-ink-2">
          Speccy posts to one channel when a review or a waiver is asked for, when a blocking thread opens, when a spec
          becomes Build Ready, and when a budget reaches 80%. A Private bundle posts nothing. Pull requests notify
          through GitHub.
        </p>
        {w.configured ? (
          <div className="flex flex-wrap items-center gap-2">
            <span>
              {w.kind === "teams" ? "Teams" : "Slack"} webhook ending in <code>{w.url_last4}</code>
            </span>
            <Button size="sm" disabled={test.isPending} onClick={() => test.mutate({})}>
              Send a test message
            </Button>
            {w.from_env ? null : (
              <Button size="sm" variant="ghost" disabled={remove.isPending} onClick={() => remove.mutate({})}>
                Remove
              </Button>
            )}
            {test.data ? (
              <span className={test.data.error ? "text-bad" : "text-ok"}>
                {test.data.status ? `HTTP ${test.data.status}` : "No answer"}
                {test.data.error ? `: ${test.data.error}` : ""}
              </span>
            ) : null}
          </div>
        ) : null}
        {w.from_env ? (
          <p className="text-xs text-ink-3">SPECCY_NOTIFY_WEBHOOK_URL sets the webhook, so it cannot change here.</p>
        ) : (
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate({ body: { kind, url: url.trim() } });
            }}
          >
            <div>
              <Label htmlFor="notify-kind">Kind</Label>
              <select
                id="notify-kind"
                value={kind}
                onChange={(e) => setKind(e.target.value as "slack" | "teams")}
                className="h-8 rounded-md border border-line-strong bg-surface px-1.5 text-sm"
              >
                <option value="slack">Slack</option>
                <option value="teams">Teams</option>
              </select>
            </div>
            <div className="min-w-64 flex-1">
              <Label htmlFor="notify-url">{w.configured ? "New webhook URL" : "Webhook URL"}</Label>
              <Input
                id="notify-url"
                type="password"
                autoComplete="off"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://hooks.slack.com/services/…"
              />
            </div>
            <Button type="submit" size="sm" disabled={!url.trim() || save.isPending}>
              Save
            </Button>
          </form>
        )}
        {save.isError ? <ErrorState message={problemMessage(save.error)} /> : null}
        {remove.isError ? <ErrorState message={problemMessage(remove.error)} /> : null}
        {test.isError ? <ErrorState message={problemMessage(test.error)} /> : null}
      </div>
    </Section>
  );
}
