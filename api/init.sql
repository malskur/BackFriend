ALTER USER "postgres" WITH PASSWORD 'test';
CREATE DATABASE Games;
GRANT ALL PRIVILEGES ON DATABASE Games to postgres;
CREATE TABLE players (
  playerid   varchar(255) NOT NULL,
  balance   integer NOT NULL
);
CREATE TABLE tournaments (
  tourid   varchar(255) NOT NULL,
  deposit   integer NOT NULL,
  playerid   varchar(255)
);
CREATE TABLE joinings (
  tourid   varchar(255) NOT NULL,
  playerid   varchar(255) NOT NULL,
  contribute   integer NOT NULL,
  contributeto   varchar(255) NOT NULL
);
ALTER TABLE tournaments ADD PRIMARY KEY (tourid);
ALTER TABLE players ADD PRIMARY KEY (playerid);
