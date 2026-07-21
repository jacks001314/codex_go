import java.util.Arrays;

public class quicksort {
    public static void sort(int[] numbers) {
        if (numbers == null || numbers.length < 2) {
            return;
        }
        quickSort(numbers, 0, numbers.length - 1);
    }

    private static void quickSort(int[] numbers, int low, int high) {
        if (low >= high) {
            return;
        }

        int pivotIndex = partition(numbers, low, high);
        quickSort(numbers, low, pivotIndex - 1);
        quickSort(numbers, pivotIndex + 1, high);
    }

    private static int partition(int[] numbers, int low, int high) {
        int pivot = numbers[high];
        int smallerIndex = low;

        for (int current = low; current < high; current++) {
            if (numbers[current] <= pivot) {
                swap(numbers, smallerIndex, current);
                smallerIndex++;
            }
        }

        swap(numbers, smallerIndex, high);
        return smallerIndex;
    }

    private static void swap(int[] numbers, int first, int second) {
        int temporary = numbers[first];
        numbers[first] = numbers[second];
        numbers[second] = temporary;
    }

    public static void main(String[] args) {
        int[] numbers;

        if (args.length == 0) {
            numbers = new int[] { 8, 3, 1, 7, 0, 10, 2 };
        } else {
            numbers = new int[args.length];
            try {
                for (int i = 0; i < args.length; i++) {
                    numbers[i] = Integer.parseInt(args[i]);
                }
            } catch (NumberFormatException exception) {
                System.err.println("请输入有效的整数。");
                return;
            }
        }

        System.out.println("排序前: " + Arrays.toString(numbers));
        sort(numbers);
        System.out.println("排序后: " + Arrays.toString(numbers));
    }
}
