package zfs

var _ ZFSManager = (*CLIManager)(nil)
var _ ZFSManager = (*MockManager)(nil)
