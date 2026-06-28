-- Add name and created_by columns to conversations
ALTER TABLE conversations ADD COLUMN name VARCHAR(100) NULL AFTER type;
ALTER TABLE conversations ADD COLUMN created_by VARCHAR(64) NULL AFTER name;

-- Users table: registered participants in the system
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) PRIMARY KEY,
    username VARCHAR(100) NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- channel_members: many-to-many mapping of users to channels (conversations)
-- Composite PK prevents duplicate memberships
-- idx_user_channels enables fast "which channels does this user belong to?" lookups
CREATE TABLE IF NOT EXISTS channel_members (
    channel_id VARCHAR(64) NOT NULL,
    user_id    VARCHAR(64) NOT NULL,
    joined_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (channel_id, user_id),
    FOREIGN KEY (channel_id) REFERENCES conversations(id),
    FOREIGN KEY (user_id) REFERENCES users(id),
    INDEX idx_user_channels (user_id)
);
