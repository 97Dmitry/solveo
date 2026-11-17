import { z } from "zod";
import type { meeting } from "../../wailsjs/go/models";
import type { Wire } from "./state";
const num = z.number().finite().nonnegative();
export const meetingStateSchema = z.object({
  recording_id: z.string().max(64),
  processing_id: z.string().max(64),
  stage: z.string().max(32),
  completed: num,
  total: num,
  error: z.string().max(8192),
  notice: z.string().max(8192),
  paused: z
    .object({
      id: z.string().max(64),
      kind: z.enum(["transcribe", "summary"]),
      partial: z.boolean(),
      retry_at: z.string().max(64),
      reason: z.string().max(8192),
    })
    .optional(),
  revision: num,
}) satisfies z.ZodType<Wire<meeting.State>>;
export const emptyMeetingState = {
  recording_id: "",
  processing_id: "",
  stage: "",
  completed: 0,
  total: 0,
  error: "",
  notice: "",
  revision: 0,
};
const infoFields = {
  id: z.string().regex(/^[a-f0-9]{32}$/),
  chat_id: z.string().max(64),
  title: z.string().max(800),
  started_at: z.string(),
  ended_at: z.string(),
  status: z.enum(["recording", "recorded", "interrupted"]),
  error: z.string().max(8192),
  duration: num,
  bytes: num,
  revision: num,
};
export const meetingInfoSchema = z.object(infoFields) satisfies z.ZodType<Wire<meeting.Info>>;
const strings = z.array(z.string().max(100000));
const item = z.object({
  text: z.string().max(100000),
  owner: z.string().max(10000),
  deadline: z.string().max(10000),
  sources: z.array(z.string().max(256)).max(1000),
}) satisfies z.ZodType<Wire<meeting.Item>>;
const summary = z.object({
  overview: z.string().max(100000),
  topics: strings,
  decisions: z.array(item).max(1000),
  tasks: z.array(item).max(1000),
  questions: strings,
  partial: z.boolean(),
  revision: num,
  model: z.string().max(128),
}) satisfies z.ZodType<Wire<meeting.Summary>>;
export const meetingDocumentSchema = z.object({
  ...infoFields,
  version: z.literal(1),
  tracks: z.array(z.string()).max(2),
  parts: z
    .array(
      z.object({
        file: z.string().regex(/^(system|mic)-\d{4}\.wav$/),
        track: z.string(),
        rate: num,
        offset: num,
        samples: num,
        complete: z.boolean(),
        transcribed: z.boolean(),
      }),
    )
    .max(400),
  segments: z
    .array(
      z.object({
        id: z.string().max(256),
        track: z.string(),
        part: z.string().max(64),
        speaker: z.string().max(128),
        start: num,
        end: num,
        text: z.string().max(262144),
      }),
    )
    .max(300000),
  summary: summary.nullish().transform((value) => value ?? undefined),
  leaves: z
    .record(z.string(), summary)
    .nullish()
    .transform((value) => value ?? {}),
}) satisfies z.ZodType<Wire<meeting.Document>>;
export type MeetingState = z.infer<typeof meetingStateSchema>;
export type MeetingInfo = z.infer<typeof meetingInfoSchema>;
export type MeetingDocument = z.infer<typeof meetingDocumentSchema>;
export type MeetingCommand = Wire<import("../../wailsjs/go/models").app.MeetingCommand>;
