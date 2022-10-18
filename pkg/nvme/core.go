package nvme

func nvmeVS(major, minor, tertiary int) int {
	return (((major) << 16) | ((minor) << 8) | (tertiary))
}
