CREATE TABLE `connections` (
	`id` text PRIMARY KEY NOT NULL,
	`user_id` text,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE cascade ON DELETE cascade
);
--> statement-breakpoint
CREATE TABLE `sessions` (
	`id` text PRIMARY KEY NOT NULL,
	`player_one` text,
	`player_two` text,
	FOREIGN KEY (`player_one`) REFERENCES `users`(`id`) ON UPDATE cascade ON DELETE cascade,
	FOREIGN KEY (`player_two`) REFERENCES `users`(`id`) ON UPDATE cascade ON DELETE cascade
);
--> statement-breakpoint
CREATE TABLE `users` (
	`id` text PRIMARY KEY NOT NULL,
	`display_name` text NOT NULL,
	`secret` text NOT NULL,
	`session_id` text,
	`connection_id` text,
	FOREIGN KEY (`session_id`) REFERENCES `sessions`(`id`) ON UPDATE cascade ON DELETE set null,
	FOREIGN KEY (`connection_id`) REFERENCES `connections`(`id`) ON UPDATE cascade ON DELETE set null
);
