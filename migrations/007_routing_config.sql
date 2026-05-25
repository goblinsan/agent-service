-- routing_config persists the runtime chat/automation node assignment that
-- the agent-service applies to llm-service routing.  It is a singleton row
-- (id is fixed at 1) so reads and writes are unambiguous.
CREATE TABLE IF NOT EXISTS routing_config (
    id              integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    chat_node       text NOT NULL DEFAULT '',
    automation_node text NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now()
);
