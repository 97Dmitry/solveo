# Main screen composition

- App wires validated snapshots and SolveoClient actions; business decisions stay in Go. Bind async file imports to the chat ID from the triggering render, never a later active chat.
- Queue/history/gallery mutations require idle submit eligibility, no pending client action and capacity for additions. Historical reuse never copies the old question or replaces current text/reference. Snapshot order drives thumbnails and numbering.
- Keep the next draft editable during Submit acknowledgement and generation; other client actions may lock it while they serialize. Run conversation.test.tsx after changing these guards or action wiring.
