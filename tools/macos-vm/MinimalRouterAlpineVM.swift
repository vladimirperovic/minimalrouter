import Foundation
import Virtualization

@main
struct MinimalRouterAlpineVM {
    @MainActor
    static func main() async throws {
        guard CommandLine.arguments.count == 5 else {
            FileHandle.standardError.write(
                Data("usage: vm-runner KERNEL INITRAMFS ISO REPOSITORY\n".utf8)
            )
            exit(64)
        }

        let kernelURL = URL(fileURLWithPath: CommandLine.arguments[1])
        let initramfsURL = URL(fileURLWithPath: CommandLine.arguments[2])
        let isoURL = URL(fileURLWithPath: CommandLine.arguments[3])
        let repositoryURL = URL(fileURLWithPath: CommandLine.arguments[4])

        let configuration = VZVirtualMachineConfiguration()
        configuration.cpuCount = 2
        configuration.memorySize = 2 * 1024 * 1024 * 1024

        let bootLoader = VZLinuxBootLoader(kernelURL: kernelURL)
        bootLoader.initialRamdiskURL = initramfsURL
        bootLoader.commandLine = "modules=loop,squashfs,sd-mod,usb-storage quiet console=hvc0"
        configuration.bootLoader = bootLoader

        let isoAttachment = try VZDiskImageStorageDeviceAttachment(url: isoURL, readOnly: true)
        configuration.storageDevices = [
            VZUSBMassStorageDeviceConfiguration(attachment: isoAttachment)
        ]

        let network = VZVirtioNetworkDeviceConfiguration()
        network.attachment = VZNATNetworkDeviceAttachment()
        network.macAddress = VZMACAddress.randomLocallyAdministered()
        configuration.networkDevices = [network]

        let serial = VZVirtioConsoleDeviceSerialPortConfiguration()
        serial.attachment = VZFileHandleSerialPortAttachment(
            fileHandleForReading: .standardInput,
            fileHandleForWriting: .standardOutput
        )
        configuration.serialPorts = [serial]
        configuration.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
        configuration.memoryBalloonDevices = [
            VZVirtioTraditionalMemoryBalloonDeviceConfiguration()
        ]

        let fileSystem = VZVirtioFileSystemDeviceConfiguration(tag: "minimalrouter")
        fileSystem.share = VZSingleDirectoryShare(
            directory: VZSharedDirectory(url: repositoryURL, readOnly: true)
        )
        configuration.directorySharingDevices = [fileSystem]

        try configuration.validate()
        let machine = VZVirtualMachine(configuration: configuration)
        try await machine.start()

        FileHandle.standardError.write(
            Data("VM started; use the serial console. Press Ctrl-C to stop.\n".utf8)
        )
        while machine.state == .running || machine.state == .starting {
            try await Task.sleep(for: .seconds(1))
        }
        FileHandle.standardError.write(
            Data("VM stopped with state \(machine.state.rawValue)\n".utf8)
        )
    }
}
