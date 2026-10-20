// Preloaded with node -r: the process looks as if it ran as root.
process.geteuid = () => 0
