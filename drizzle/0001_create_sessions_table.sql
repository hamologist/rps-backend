CREATE TABLE `sessions` (
	`id` text PRIMARY KEY NOT NULL,
	`player_one` text,
	`player_two` text,
	FOREIGN KEY (`player_one`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`player_two`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE no action
);
